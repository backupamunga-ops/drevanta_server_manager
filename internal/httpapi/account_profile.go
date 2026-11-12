// Account profile endpoints: email binding and password change.
//
// The email is the second login identifier (next to the username) and,
// from v0.4.17 on, the delivery target of password-reset links. It is
// captured at claim time and changeable here; uniqueness is enforced
// case-insensitively across accounts.
//
// Password change requires the CURRENT password (a stolen session alone
// cannot lock the operator out), enforces the same 12..128 policy as
// sign-up, and ends every OTHER session — the device that changed the
// password keeps its session, all other devices must re-authenticate.
package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]{2,}$`)

const (
	maxEmailLen = 254
)

func validEmail(e string) bool {
	return len(e) <= maxEmailLen && emailRE.MatchString(e)
}

// handleAccountProfile reports the operator account (username + email).
func (s *server) handleAccountProfile(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if sess == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	u, err := s.st.UserByID(r.Context(), sess.rec.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "account unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username,
		"email":    u.Email,
	})
}

type accountEmailRequest struct {
	Email string `json:"email"`
}

// handleAccountEmailSet binds or replaces the account email. An empty
// email clears it. Strictly rate limited like the other auth-adjacent
// endpoints (the "mfa" bucket: small, per-client).
func (s *server) handleAccountEmailSet(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "mfa")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	sess := sessionFrom(r.Context())
	if sess == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	var req accountEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email := strings.TrimSpace(req.Email)
	if email != "" && !validEmail(email) {
		writeError(w, http.StatusBadRequest, "invalid_input", "enter a valid email address")
		return
	}
	if err := s.st.SetUserEmail(r.Context(), sess.rec.UserID, email); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			writeError(w, http.StatusConflict, "email_taken", "that email is already in use")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "could not save the email")
		return
	}
	if email == "" {
		audit(r.Context(), s.st, sess.username, "email_removed", "user", nil)
	} else {
		audit(r.Context(), s.st, sess.username, "email_changed", "user", nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": email})
}

type accountPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleAccountPassword changes the operator password: current password
// must verify (argon2id under the bounded-concurrency guard), the new
// one follows the sign-up policy, and every OTHER session is ended.
func (s *server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(clientKey(r, "mfa")) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
		return
	}
	sess := sessionFrom(r.Context())
	if sess == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	var req accountPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "current and new password required")
		return
	}
	if !validPassword(req.NewPassword) {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"new password must be at least 12 characters (max 128)")
		return
	}

	user, err := s.st.UserByID(r.Context(), sess.rec.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "account unavailable")
		return
	}

	// Bounded argon2id concurrency, same as login.
	select {
	case s.argonSlots <- struct{}{}:
		defer func() { <-s.argonSlots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "busy", "too many concurrent operations")
		return
	}
	if auth.VerifyPassword(req.CurrentPassword, user.PasswordHash) != nil {
		audit(r.Context(), s.st, sess.username, "password_change_failed", "user",
			map[string]any{"reason": "current_password_invalid"})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not hash password")
		return
	}
	if err := s.st.UpdatePasswordHash(r.Context(), user.ID, newHash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not save the password")
		return
	}

	// End every other session; the current device stays signed in.
	ended, err := s.st.DeleteOtherSessions(r.Context(), user.ID, auth.TokenHash(sess.raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "password saved, but other sessions could not be ended")
		return
	}
	audit(r.Context(), s.st, sess.username, "password_changed", "user",
		map[string]any{"other_sessions_ended": ended})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "other_sessions_ended": ended})
}
