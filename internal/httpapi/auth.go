// Identity and first-owner onboarding endpoints.
//
// Onboarding state machine (instance_state, single row):
//
//	unclaimed --claim(token+credentials)--> mfa_pending
//	mfa_pending --mfa/verify--> active (mfa_mode=totp)
//	mfa_pending --mfa/skip----> active (mfa_mode=skipped)
//
// Security properties enforced here:
//   - The claim token is single-use, expiring, and stored hashed; a
//     rejected claim never reveals which part was wrong beyond the
//     token reason (needed for operator diagnostics).
//   - Only the SHA-256 of session cookies and recovery codes is stored.
//   - Passwords are argon2id; logins and claim attempts are audited with
//     reasons but never with secret values.
//   - Unauthenticated endpoints are rate limited per source address.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

const (
	minUsernameLen = 3
	maxUsernameLen = 32
	minPasswordLen = 12
	maxPasswordLen = 128
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionSliding.Seconds()),
		// Secure when the request arrived over TLS or through the trusted
		// public HTTPS proxy (the only peer that can set X-Forwarded-Proto
		// on the loopback listener in practice). The SSH-tunnel path stays
		// non-Secure so it keeps working unchanged.
		Secure: requestIsHTTPS(r),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Secure:   requestIsHTTPS(r),
	})
}

// requestIsHTTPS decides whether the browser-side scheme is https. The
// trusted-proxy boundary lives in the httpsd proxy: it strips every
// client-supplied forwarding header and sets X-Forwarded-Proto itself.
// Direct loopback connections without the header keep the tunnel
// behaviour (non-Secure cookies over http://127.0.0.1).
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func validUsername(u string) bool {
	return len(u) >= minUsernameLen && len(u) <= maxUsernameLen && usernameRE.MatchString(u)
}

func validPassword(p string) bool {
	return len(p) >= minPasswordLen && len(p) <= maxPasswordLen
}

// newSession creates the DB session and sets the cookie.
func (s *server) newSession(w http.ResponseWriter, r *http.Request, userID int64, scope string) error {
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if _, err := s.st.CreateSession(r.Context(), hash, userID, scope, sessionSliding, sessionAbsolute); err != nil {
		return err
	}
	setSessionCookie(w, r, token)
	return nil
}

func audit(ctx context.Context, st *store.Store, actor, action, target string, detail map[string]any) {
	if err := st.Audit(ctx, actor, action, target, detail); err != nil {
		slog.Error("audit write failed", "action", action, "err", err)
	}
}

// ---------------------------------------------------------------------------
// Onboarding status (public)
// ---------------------------------------------------------------------------

func (s *server) handleOnboardingStatus(w http.ResponseWriter, r *http.Request) {
	state, err := s.st.InstanceState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "state unavailable")
		return
	}
	tokenActive := false
	if ct, err := s.st.ActiveClaimToken(r.Context()); err == nil && ct.ExpiresAt.After(time.Now().UTC()) {
		tokenActive = true
	}
	resp := map[string]any{
		"instance_status":    state.Status,
		"mfa_mode":           state.MFAMode,
		"claim_token_active": tokenActive,
	}
	sess, serr := s.resolveSession(r)
	if serr != nil {
		writeError(w, http.StatusInternalServerError, "internal", "session lookup failed")
		return
	}
	if sess != nil {
		resp["authenticated"] = true
		resp["scope"] = sess.rec.Scope
		resp["username"] = sess.username
	} else {
		resp["authenticated"] = false
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Claim (public, rate limited)
// ---------------------------------------------------------------------------

type claimRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "claim")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req claimRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	state, err := s.st.InstanceState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "state unavailable")
		return
	}
	if state.Status != store.StatusUnclaimed {
		writeError(w, http.StatusConflict, "wrong_state",
			"this instance has already been claimed; sign in instead")
		return
	}

	if req.Token == "" || !validUsername(req.Username) || !validPassword(req.Password) {
		audit(r.Context(), s.st, "onboarding", "claim_rejected", "claim",
			map[string]any{"reason": "invalid_input"})
		writeError(w, http.StatusBadRequest, "invalid_input",
			"token required, username 3-32 chars [a-zA-Z0-9._-], password at least 12 chars")
		return
	}

	tokenHash := auth.TokenHash(req.Token)
	ok, err := s.st.ConsumeClaimToken(r.Context(), tokenHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "token check failed")
		return
	}
	if !ok {
		reason := "invalid_token"
		if ct, gerr := s.st.ClaimTokenByHash(r.Context(), tokenHash); gerr == nil {
			switch {
			case ct.ConsumedAt.IsZero() && !ct.RevokedAt.IsZero():
				reason = "invalid_token" // rotated out; indistinguishable to outsiders
			case !ct.ExpiresAt.After(time.Now().UTC()):
				reason = "token_expired"
			default:
				reason = "token_used"
			}
		}
		audit(r.Context(), s.st, "onboarding", "claim_rejected", "claim",
			map[string]any{"reason": reason})
		writeError(w, http.StatusUnauthorized, reason, "claim token rejected: "+reason)
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not hash password")
		return
	}
	user, err := s.st.CreateUser(r.Context(), req.Username, passwordHash)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "username_taken", "username already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "could not create account")
		return
	}
	if err := s.st.ClaimInstance(r.Context(), user.ID); err != nil {
		// Compensate: the account must not linger without the transition.
		_ = s.st.DeleteUser(r.Context(), user.ID)
		writeError(w, http.StatusConflict, "wrong_state", "instance state changed during claim")
		return
	}

	if err := s.newSession(w, r, user.ID, store.ScopeOnboarding); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not start session")
		return
	}
	audit(r.Context(), s.st, req.Username, "claim_succeeded", "instance", nil)
	audit(r.Context(), s.st, req.Username, "admin_created", "user", map[string]any{"username": req.Username})
	writeJSON(w, http.StatusOK, map[string]any{
		"user":            map[string]string{"username": req.Username},
		"instance_status": store.StatusMFAPending,
		"next":            "mfa",
	})
}

// ---------------------------------------------------------------------------
// MFA enrolment (session scope: onboarding, state: mfa_pending)
// ---------------------------------------------------------------------------

type mfaCodeRequest struct {
	Code string `json:"code"`
}

type mfaSkipRequest struct {
	AcknowledgeRisk bool `json:"acknowledge_risk"`
}

func (s *server) requireMFAPending(w http.ResponseWriter, r *http.Request) bool {
	state, err := s.st.InstanceState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "state unavailable")
		return false
	}
	if state.Status != store.StatusMFAPending {
		writeError(w, http.StatusConflict, "wrong_state", "onboarding is not awaiting MFA setup")
		return false
	}
	return true
}

func (s *server) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if !s.requireMFAPending(w, r) {
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not generate secret")
		return
	}
	if err := s.st.SetMFASecret(r.Context(), sess.rec.UserID, secret); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not store secret")
		return
	}
	audit(r.Context(), s.st, sess.username, "mfa_setup_started", "user", nil)
	writeJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_uri": auth.TOTPURI(secret, "Drevanta", sess.username),
	})
}

func (s *server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "mfa")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	sess := sessionFrom(r.Context())
	if !s.requireMFAPending(w, r) {
		return
	}
	var req mfaCodeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	user, err := s.st.UserByID(r.Context(), sess.rec.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "account unavailable")
		return
	}
	step, ok, err := auth.VerifyTOTP(user.MFASecret, req.Code, time.Now())
	if err != nil || !ok {
		audit(r.Context(), s.st, sess.username, "mfa_verify_failed", "user", nil)
		writeError(w, http.StatusUnauthorized, "totp_invalid", "invalid authenticator code")
		return
	}
	if err := s.st.EnableMFA(r.Context(), user.ID, step); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not enable MFA")
		return
	}
	codes, hashes, err := generateRecoveryCodes(10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not generate recovery codes")
		return
	}
	if err := s.st.ReplaceRecoveryCodes(r.Context(), user.ID, hashes); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not store recovery codes")
		return
	}
	if err := s.st.FinishOnboarding(r.Context(), "totp"); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not complete onboarding")
		return
	}
	if err := s.st.SetSessionScope(r.Context(), auth.TokenHash(sess.raw), store.ScopeFull); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not upgrade session")
		return
	}
	audit(r.Context(), s.st, sess.username, "mfa_enabled", "user", nil)
	audit(r.Context(), s.st, sess.username, "onboarding_completed", "instance", map[string]any{"mfa_mode": "totp"})
	writeJSON(w, http.StatusOK, map[string]any{
		"recovery_codes": codes,
		"next":           "done",
	})
}

func (s *server) handleMFASkip(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if !s.requireMFAPending(w, r) {
		return
	}
	var req mfaSkipRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !req.AcknowledgeRisk {
		writeError(w, http.StatusBadRequest, "risk_not_acknowledged",
			"skipping MFA requires acknowledging the risk explicitly")
		return
	}
	if err := s.st.FinishOnboarding(r.Context(), "skipped"); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not complete onboarding")
		return
	}
	if err := s.st.SetSessionScope(r.Context(), auth.TokenHash(sess.raw), store.ScopeFull); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not upgrade session")
		return
	}
	audit(r.Context(), s.st, sess.username, "mfa_skipped", "user",
		map[string]any{"acknowledged_risk": true})
	audit(r.Context(), s.st, sess.username, "onboarding_completed", "instance", map[string]any{"mfa_mode": "skipped"})
	writeJSON(w, http.StatusOK, map[string]any{"mfa_mode": "skipped", "next": "done"})
}

func generateRecoveryCodes(n int) ([]string, []string, error) {
	codes := make([]string, 0, n)
	hashes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		code, err := auth.NewRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		codes = append(codes, code)
		hashes = append(hashes, auth.HashRecoveryCode(auth.NormalizeRecoveryCode(code)))
	}
	return codes, hashes, nil
}

// ---------------------------------------------------------------------------
// Login / logout / session info
// ---------------------------------------------------------------------------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTP     string `json:"totp"`
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "login")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	state, err := s.st.InstanceState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "state unavailable")
		return
	}
	if state.Status == store.StatusUnclaimed {
		writeError(w, http.StatusConflict, "instance_unclaimed",
			"no administrator exists yet; use the claim token from the installer")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "username and password required")
		return
	}

	user, err := s.st.UserByUsername(r.Context(), req.Username)
	if errors.Is(err, store.ErrNotFound) {
		audit(r.Context(), s.st, req.Username, "login_failed", "auth", map[string]any{"reason": "invalid_credentials"})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "account unavailable")
		return
	}

	// Bounded argon2id concurrency (memory-safety backstop; see
	// argonConcurrency). Constant-time response regardless of outcome.
	select {
	case s.argonSlots <- struct{}{}:
		defer func() { <-s.argonSlots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "busy", "too many concurrent sign-in attempts")
		return
	}
	if auth.VerifyPassword(req.Password, user.PasswordHash) != nil {
		audit(r.Context(), s.st, req.Username, "login_failed", "auth", map[string]any{"reason": "invalid_credentials"})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}

	// Second factor when MFA is active. A 6-digit value is treated as a
	// TOTP code; anything else as a single-use recovery code.
	if state.Status == store.StatusActive && user.MFAEnabled {
		code := strings.TrimSpace(req.TOTP)
		if code == "" {
			writeError(w, http.StatusUnauthorized, "totp_required", "authenticator code required")
			return
		}
		if sixDigitRE.MatchString(code) {
			step, ok, err := auth.VerifyTOTP(user.MFASecret, code, time.Now())
			if err != nil || !ok {
				audit(r.Context(), s.st, req.Username, "login_failed", "auth", map[string]any{"reason": "totp_invalid"})
				writeError(w, http.StatusUnauthorized, "totp_invalid", "invalid authenticator code")
				return
			}
			_ = step // matched step; kept for future replay-hardening
		} else {
			used, err := s.st.ConsumeRecoveryCode(r.Context(), user.ID, auth.HashRecoveryCode(auth.NormalizeRecoveryCode(code)))
			if err != nil || !used {
				audit(r.Context(), s.st, req.Username, "login_failed", "auth", map[string]any{"reason": "recovery_invalid"})
				writeError(w, http.StatusUnauthorized, "recovery_invalid", "invalid recovery code")
				return
			}
			audit(r.Context(), s.st, req.Username, "recovery_code_used", "auth", nil)
		}
	}

	// A paused onboarding (browser closed after claim) resumes with an
	// onboarding-scoped session; a completed instance gets full scope.
	scope := store.ScopeFull
	if state.Status == store.StatusMFAPending {
		scope = store.ScopeOnboarding
	}
	if err := s.newSession(w, r, user.ID, scope); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not start session")
		return
	}
	audit(r.Context(), s.st, req.Username, "login_success", "auth", map[string]any{"scope": scope})
	writeJSON(w, http.StatusOK, map[string]any{
		"user":            map[string]any{"username": user.Username, "mfa_enabled": user.MFAEnabled},
		"instance_status": state.Status,
		"scope":           scope,
	})
}

var sixDigitRE = regexp.MustCompile(`^\d{6}$`)

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, err := s.resolveSession(r)
	if err == nil && sess != nil {
		if derr := s.st.DeleteSession(r.Context(), auth.TokenHash(sess.raw)); derr != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not end session")
			return
		}
		audit(r.Context(), s.st, sess.username, "logout", "auth", nil)
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	state, err := s.st.InstanceState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "state unavailable")
		return
	}
	resp := map[string]any{
		"authenticated":   false,
		"instance_status": state.Status,
		"mfa_enabled":     false,
		"username":        "",
		"scope":           "",
	}
	// This endpoint resolves the session itself: it must answer for both
	// anonymous and authenticated callers.
	sess, serr := s.resolveSession(r)
	if serr != nil {
		writeError(w, http.StatusInternalServerError, "internal", "session lookup failed")
		return
	}
	if sess != nil {
		s.touch(sess)
		resp["authenticated"] = true
		resp["username"] = sess.username
		resp["scope"] = sess.rec.Scope
		resp["mfa_enabled"] = sess.mfaOn
	}
	writeJSON(w, http.StatusOK, resp)
}

// decodeJSON parses a request body of at most 16 KiB into v.
func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}
