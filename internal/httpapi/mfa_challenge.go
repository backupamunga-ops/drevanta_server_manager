// Two-step sign-in: MFA challenges.
//
// When the password verifies but the account has MFA enabled and the
// client did not present a code, login answers 401 totp_required with a
// short-lived mfa_challenge token. The browser keeps that token in
// sessionStorage (surviving a mobile page reload while the user fetches
// the code from their authenticator app) and completes the sign-in
// through POST /api/v1/auth/login/mfa with {challenge, code}.
//
// Threat model: the challenge grants NO access by itself — it only
// narrows what remains to prove to "a valid TOTP or recovery code". It
// is issued exclusively after a full argon2id password verification,
// it is stored hashed, single-use, capped in attempts and lifetime, and
// it never reaches disk or logs.
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

const (
	mfaChallengeTTL      = 5 * time.Minute
	mfaChallengeMaxTries = 5
	// Hard cap on live challenges: each is ~200 bytes, so even a flood
	// (rate limited to 10/min/IP anyway) cannot grow this unbounded.
	mfaChallengeMaxLive = 1000
)

// mfaChallenge is the server-side half of a two-step sign-in.
type mfaChallenge struct {
	userID    int64
	username  string
	expiresAt time.Time
	attempts  int
}

// mfaChallengeStore keeps the live challenges in memory only. A daemon
// restart simply expires every outstanding challenge; the user signs in
// from the credentials step again.
type mfaChallengeStore struct {
	mu   sync.Mutex
	live map[string]*mfaChallenge // key: SHA-256 hex of the token
	ttl  time.Duration
	max  int
	now  func() time.Time // injectable clock for tests
}

func newMFAChallengeStore() *mfaChallengeStore {
	return &mfaChallengeStore{
		live: make(map[string]*mfaChallenge),
		ttl:  mfaChallengeTTL,
		max:  mfaChallengeMaxTries,
		now:  time.Now,
	}
}

func challengeHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// issue mints a fresh opaque token, records its hash and returns the
// token for the client. Expired entries are swept opportunistically.
func (m *mfaChallengeStore) issue(userID int64, username string) (string, error) {
	token, hash, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for k, ch := range m.live {
		if now.After(ch.expiresAt) {
			delete(m.live, k)
		}
	}
	if len(m.live) >= mfaChallengeMaxLive {
		return "", errChallengeFull
	}
	m.live[hash] = &mfaChallenge{
		userID:    userID,
		username:  username,
		expiresAt: now.Add(m.ttl),
	}
	return token, nil
}

var errChallengeFull = challengeStoreError("too many outstanding sign-in attempts")

type challengeStoreError string

func (e challengeStoreError) Error() string { return string(e) }

// peek resolves a presented token to a live challenge. Expired or
// unknown tokens yield ok=false.
func (m *mfaChallengeStore) peek(token string) (*mfaChallenge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.live[challengeHash(token)]
	if !ok {
		return nil, false
	}
	if m.now().After(ch.expiresAt) {
		delete(m.live, challengeHash(token))
		return nil, false
	}
	return ch, true
}

// countAttempt consumes one of the challenge's allowed wrong-code
// attempts. It reports whether the challenge is now exhausted (in which
// case it has been dropped and the client must start over).
func (m *mfaChallengeStore) countAttempt(token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash := challengeHash(token)
	ch, ok := m.live[hash]
	if !ok {
		return true // treat unknown as exhausted: the next peek fails anyway
	}
	ch.attempts++
	if ch.attempts >= m.max {
		delete(m.live, hash)
		return true
	}
	return false
}

// drop removes a challenge (single-use on success, or explicit discard).
func (m *mfaChallengeStore) drop(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.live, challengeHash(token))
}

// ---------------------------------------------------------------------------
// Endpoint
// ---------------------------------------------------------------------------

type mfaLoginRequest struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

// handleLoginMFA completes a two-step sign-in: the challenge proves the
// password was already verified, the code proves the second factor.
func (s *server) handleLoginMFA(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "login")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	var req mfaLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	token := strings.TrimSpace(req.Challenge)
	code := strings.TrimSpace(req.Code)
	if token == "" || code == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "challenge and code required")
		return
	}
	ch, ok := s.challenges.peek(token)
	if !ok {
		writeError(w, http.StatusUnauthorized, "challenge_invalid",
			"this sign-in attempt expired — enter your username and password again")
		return
	}
	user, err := s.st.UserByID(r.Context(), ch.userID)
	if err != nil {
		s.challenges.drop(token)
		writeError(w, http.StatusInternalServerError, "internal", "account unavailable")
		return
	}
	failKey := "login:" + user.Username
	if s.fails.Locked(failKey) {
		s.challenges.drop(token)
		audit(r.Context(), s.st, user.Username, "login_failed", "auth", map[string]any{"reason": "account_locked"})
		writeError(w, http.StatusTooManyRequests, "account_locked", "too many failed attempts for this account \u2014 wait a few minutes")
		return
	}

	state, err := s.st.InstanceState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "state unavailable")
		return
	}

	// Same second-factor rules as the combined login: a 6-digit value is
	// a TOTP code, anything else a single-use recovery code.
	if sixDigitRE.MatchString(code) {
		step, ok, err := auth.VerifyTOTP(user.MFASecret, code, time.Now())
		if err != nil || !ok {
			s.failMFAAttempt(w, r, token, user.Username, "totp_invalid", "invalid authenticator code")
			return
		}
		if step < user.MFALastStep {
			s.failMFAAttempt(w, r, token, user.Username, "totp_invalid", "invalid authenticator code")
			return
		}
		if err := s.st.AcceptMFAStep(r.Context(), user.ID, step); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not record the code step")
			return
		}
	} else {
		used, err := s.st.ConsumeRecoveryCode(r.Context(), user.ID,
			auth.HashRecoveryCode(auth.NormalizeRecoveryCode(code)))
		if err != nil || !used {
			s.failMFAAttempt(w, r, token, user.Username, "recovery_invalid", "invalid recovery code")
			return
		}
		audit(r.Context(), s.st, user.Username, "recovery_code_used", "auth", nil)
	}

	// Success: the challenge is single-use, whatever the outcome after
	// this point (a failed session creation forces a fresh sign-in).
	s.challenges.drop(token)

	scope := store.ScopeFull
	if state.Status == store.StatusMFAPending {
		scope = store.ScopeOnboarding
	}
	if err := s.newSession(w, r, user.ID, scope); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not start session")
		return
	}
	s.fails.Reset(failKey)
	audit(r.Context(), s.st, user.Username, "login_success", "auth",
		map[string]any{"scope": scope, "via": "mfa_challenge"})
	writeJSON(w, http.StatusOK, map[string]any{
		"user":            map[string]any{"username": user.Username, "mfa_enabled": user.MFAEnabled},
		"instance_status": state.Status,
		"scope":           scope,
	})
}

// failMFAAttempt audits a wrong code and burns one attempt. When the
// attempts run out the challenge dies and the client must re-enter
// credentials — an attacker who stolen a live challenge gets at most
// mfaChallengeMaxTries code guesses out of it.
func (s *server) failMFAAttempt(w http.ResponseWriter, r *http.Request, token, username, errCode, message string) {
	audit(r.Context(), s.st, username, "login_failed", "auth", map[string]any{"reason": errCode})
	if s.challenges.countAttempt(token) {
		writeError(w, http.StatusUnauthorized, "challenge_exhausted",
			"too many wrong codes — enter your username and password again")
		return
	}
	writeError(w, http.StatusUnauthorized, errCode, message)
}
