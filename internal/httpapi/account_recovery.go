// Account recovery (v0.4.17): the three ways back in.
//
//	Path A — email reset link:  POST /auth/forgot -> POST /auth/reset
//	Path B — recovery-code sign-in: POST /auth/recovery-login, then the
//	         forced new-password screen (/account/password accepts the
//	         limited "recovery" session scope)
//	Path C — break-glass CLI:  drevantad admin reset-password (root on
//	         the box)
//
// Anti-enumeration: /auth/forgot always answers the same generic ok,
// whatever the identifier; every failing path looks identical from the
// outside. Reset tokens are hashed, single-use, 30-minute TTL, capped
// at 3 unconsumed per account per hour, and a completed reset ends
// every existing session.
package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/mailer"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

const passwordResetTTL = 30 * time.Minute

// requireScopeIn authenticates the session and enforces that its scope
// is one of the given values.
func (s *server) requireScopeIn(scopes ...string) func(http.HandlerFunc) http.Handler {
	allowed := map[string]bool{}
	for _, sc := range scopes {
		allowed[sc] = true
	}
	return func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, err := s.resolveSession(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "session lookup failed")
				return
			}
			if sess == nil {
				writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
				return
			}
			if !allowed[sess.rec.Scope] {
				writeError(w, http.StatusForbidden, "insufficient_scope", "this action is not available for the current session")
				return
			}
			s.touch(sess)
			next(w, r.WithContext(withSession(r.Context(), sess)))
		})
	}
}

// ---------------------------------------------------------------------------
// Path B: recovery-code sign-in (no password)
// ---------------------------------------------------------------------------

type recoveryLoginRequest struct {
	Username string `json:"username"`
	Code     string `json:"code"`
}

// handleRecoveryLogin signs in with username + single-use recovery code.
// The code replaces BOTH factors; the resulting limited session may only
// set a new password.
func (s *server) handleRecoveryLogin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "login")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req recoveryLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ident := strings.TrimSpace(req.Username)
	code := strings.TrimSpace(req.Code)
	if ident == "" || code == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "username and recovery code required")
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
	user, err := s.st.UserByIdentifier(r.Context(), ident)
	if err != nil {
		audit(r.Context(), s.st, ident, "login_failed", "auth", map[string]any{"reason": "recovery_invalid"})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or recovery code")
		return
	}

	// Bounded argon2-free path: recovery codes are SHA-256 comparisons,
	// but the same constant response applies whatever fails.
	used, err := s.st.ConsumeRecoveryCode(r.Context(), user.ID,
		auth.HashRecoveryCode(auth.NormalizeRecoveryCode(code)))
	if err != nil || !used {
		audit(r.Context(), s.st, user.Username, "login_failed", "auth", map[string]any{"reason": "recovery_invalid"})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or recovery code")
		return
	}
	audit(r.Context(), s.st, user.Username, "recovery_code_used", "auth", nil)
	audit(r.Context(), s.st, user.Username, "recovery_login", "auth", nil)

	if err := s.newSession(w, r, user.ID, store.ScopeRecovery); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not start session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":            map[string]any{"username": user.Username, "mfa_enabled": user.MFAEnabled},
		"instance_status": state.Status,
		"scope":           store.ScopeRecovery,
		"next":            "password",
	})
}

// ---------------------------------------------------------------------------
// Path A: email reset link
// ---------------------------------------------------------------------------

type forgotRequest struct {
	Username string `json:"username"`
}

// handleForgotPassword starts the email reset: always answers the same
// generic response. A real mail goes out only when the account exists,
// has an email, mail delivery is configured, and the per-account hourly
// cap is not hit.
func (s *server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "login")) {
		// Same generic shape even when rate limited.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	var req forgotRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	ident := strings.TrimSpace(req.Username)
	generic := map[string]any{
		"ok":      true,
		"message": "If that account exists, has an email, and mail delivery is configured, a reset link is on its way. Check your inbox.",
	}
	if ident == "" {
		writeJSON(w, http.StatusOK, generic)
		return
	}

	user, err := s.st.UserByIdentifier(r.Context(), ident)
	if err != nil || user.Email == "" {
		writeJSON(w, http.StatusOK, generic)
		return
	}
	cfgRow, err := s.st.GetSMTPConfig(r.Context())
	if err != nil || !cfgRow.Enabled {
		writeJSON(w, http.StatusOK, generic)
		return
	}
	token, _, terr := auth.NewToken()
	if terr != nil {
		writeJSON(w, http.StatusOK, generic)
		return
	}
	if err := s.st.CreatePasswordReset(r.Context(), auth.TokenHash(token), user.ID, passwordResetTTL); err != nil {
		if errors.Is(err, store.ErrResetRateLimited) {
			slog.Warn("password reset rate limited", "username", user.Username)
		}
		writeJSON(w, http.StatusOK, generic)
		return
	}

	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	link := fmt.Sprintf("%s://%s/login#reset=%s", scheme, r.Host, token)
	cfg := mailer.Config{
		Enabled:  cfgRow.Enabled,
		Host:     cfgRow.Host,
		Port:     cfgRow.Port,
		Username: cfgRow.Username,
		Password: cfgRow.Password,
		FromAddr: cfgRow.FromAddr,
	}
	body := strings.Join([]string{
		"Hello " + user.Username + ",",
		"",
		"A password reset was requested for the Drevanta server console at " + r.Host + ".",
		"Open this single-use link within 30 minutes to choose a new password:",
		"",
		link,
		"",
		"If you did not request this, ignore this message — the link expires on its own.",
	}, "\n")
	if serr := mailer.Send(cfg, user.Email, "Drevanta password reset", body); serr != nil {
		slog.Error("password reset mail failed", "err", serr)
		audit(r.Context(), s.st, user.Username, "password_reset_email_failed", "auth",
			map[string]any{"error": serr.Error()})
	} else {
		audit(r.Context(), s.st, user.Username, "password_reset_requested", "auth", nil)
	}
	writeJSON(w, http.StatusOK, generic)
}

type resetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// handlePasswordReset completes the email reset: consume the token, set
// the new password, end every session.
func (s *server) handlePasswordReset(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "login")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req resetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" || !validPassword(req.NewPassword) {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"reset token and a new password of at least 12 characters are required")
		return
	}
	userID, err := s.st.ConsumePasswordReset(r.Context(), auth.TokenHash(token))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "reset_invalid",
			"this reset link is invalid, used, or expired — request a new one")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not hash password")
		return
	}
	if err := s.st.UpdatePasswordHash(r.Context(), userID, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not save the password")
		return
	}
	user, err := s.st.UserByID(r.Context(), userID)
	if err == nil {
		audit(r.Context(), s.st, user.Username, "password_reset", "auth", nil)
	}
	ended, _ := s.st.DeleteAllSessions(r.Context(), userID)
	_ = ended
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// SMTP configuration (full scope) + test send
// ---------------------------------------------------------------------------

type smtpRequest struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"` // empty = keep the stored one
	FromAddr string `json:"from_addr"`
}

func (s *server) handleSMTPGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.st.GetSMTPConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not load mail settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      cfg.Enabled,
		"host":         cfg.Host,
		"port":         cfg.Port,
		"username":     cfg.Username,
		"from_addr":    cfg.FromAddr,
		"password_set": cfg.PasswordSet,
	})
}

func (s *server) handleSMTPSet(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "mfa")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req smtpRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	req.FromAddr = strings.TrimSpace(req.FromAddr)
	if req.Enabled {
		if req.Host == "" {
			writeError(w, http.StatusBadRequest, "invalid_input", "an SMTP host is required")
			return
		}
		if req.Port < 1 || req.Port > 65535 {
			writeError(w, http.StatusBadRequest, "invalid_input", "the SMTP port must be 1-65535")
			return
		}
		if !validEmail(req.FromAddr) {
			writeError(w, http.StatusBadRequest, "invalid_input", "enter a valid From address")
			return
		}
	}
	prev, err := s.st.GetSMTPConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not load mail settings")
		return
	}
	if req.Password == "" && req.Enabled && !prev.PasswordSet && req.Username != "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "enter the SMTP password (none stored yet)")
		return
	}
	if err := s.st.SetSMTPConfig(r.Context(), store.SMTPConfig{
		Enabled:  req.Enabled,
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		Password: req.Password,
		FromAddr: req.FromAddr,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not save mail settings")
		return
	}
	sess := sessionFrom(r.Context())
	actor := "system"
	if sess != nil {
		actor = sess.username
	}
	audit(r.Context(), s.st, actor, "smtp_changed", "smtp",
		map[string]any{"enabled": req.Enabled, "host": req.Host, "port": req.Port})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type smtpTestRequest struct {
	To string `json:"to"`
}

// handleSMTPTest sends a test message — to the From address by default.
func (s *server) handleSMTPTest(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "mfa")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req smtpTestRequest
	_ = decodeJSON(r, &req) // optional body
	to := strings.TrimSpace(req.To)
	cfgRow, err := s.st.GetSMTPConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not load mail settings")
		return
	}
	if to == "" {
		to = cfgRow.FromAddr
	}
	if !validEmail(to) {
		writeError(w, http.StatusBadRequest, "invalid_input", "enter a valid recipient address")
		return
	}
	cfg := mailer.Config{
		Enabled:  cfgRow.Enabled,
		Host:     cfgRow.Host,
		Port:     cfgRow.Port,
		Username: cfgRow.Username,
		Password: cfgRow.Password,
		FromAddr: cfgRow.FromAddr,
	}
	err = mailer.Send(cfg, to, "Drevanta test message",
		"This is a test message from your Drevanta server console. Mail delivery works.")
	if err != nil {
		sess := sessionFrom(r.Context())
		actor := "system"
		if sess != nil {
			actor = sess.username
		}
		audit(r.Context(), s.st, actor, "smtp_test_failed", "smtp", map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
