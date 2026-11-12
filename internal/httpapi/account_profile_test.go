package httpapi

// v0.4.16: account email binding + password change.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// claimAndSkip creates the admin with an email and skips MFA, leaving a
// full-scope session cookie. Returns the session cookie header map.
func claimAndSkip(t *testing.T, h http.Handler, st *store.Store, email string) map[string]string {
	t.Helper()
	token, err := newClaimToken(t, st)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"token":%q,"username":"admin","email":%q,"password":"correct horse battery"}`, token, email)
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body.String())
	}
	sess := cookieOf(rec)
	hdr := map[string]string{"Cookie": sess.Name + "=" + sess.Value}
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{"acknowledge_risk":true}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}
	return hdr
}

func TestClaimStoresEmailAndProfileShowsIt(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAndSkip(t, h, st, "op@example.com")

	rec := do(t, h, http.MethodGet, "/api/v1/account/profile", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile = %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["username"] != "admin" || m["email"] != "op@example.com" {
		t.Fatalf("profile: %v", m)
	}

	// Anonymous callers get nothing.
	rec = do(t, h, http.MethodGet, "/api/v1/account/profile", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous profile = %d", rec.Code)
	}
}

func TestLoginByEmail(t *testing.T) {
	h, st := newTestHandler(t)
	claimAndSkip(t, h, st, "Op@Example.com")

	// Mixed-case email matches case-insensitively.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"op@example.com","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login by email = %d: %s", rec.Code, rec.Body.String())
	}
	if decodeMap(t, rec)["scope"] != "full" {
		t.Fatalf("scope = %v", decodeMap(t, rec))
	}

	// Unknown identifier still reports the constant invalid_credentials.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"nobody@example.com","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "invalid_credentials" {
		t.Fatalf("unknown identifier: %d %v", rec.Code, decodeMap(t, rec))
	}
}

func TestAccountEmailSetChangeClear(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAndSkip(t, h, st, "op@example.com")

	// Invalid format refused.
	rec := do(t, h, http.MethodPost, "/api/v1/account/email", `{"email":"not-an-email"}`, hdr)
	if rec.Code != http.StatusBadRequest || errCode(decodeMap(t, rec)) != "invalid_input" {
		t.Fatalf("invalid email: %d %v", rec.Code, decodeMap(t, rec))
	}

	// Replace, then clear.
	rec = do(t, h, http.MethodPost, "/api/v1/account/email", `{"email":"new@example.com"}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/api/v1/account/profile", "", hdr)
	if decodeMap(t, rec)["email"] != "new@example.com" {
		t.Fatalf("email = %v", decodeMap(t, rec))
	}
	rec = do(t, h, http.MethodPost, "/api/v1/account/email", `{"email":""}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/account/profile", "", hdr)
	if decodeMap(t, rec)["email"] != "" {
		t.Fatalf("email after clear = %v", decodeMap(t, rec))
	}

	// Login by email no longer resolves after clearing.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"new@example.com","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login by cleared email = %d", rec.Code)
	}
}

func TestAccountPasswordChange(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAndSkip(t, h, st, "op@example.com")

	// A second login creates the "other device" session.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second login = %d", rec.Code)
	}
	other := cookieOf(rec)
	otherHdr := map[string]string{"Cookie": other.Name + "=" + other.Value}

	// Wrong current password is refused.
	rec = do(t, h, http.MethodPost, "/api/v1/account/password",
		`{"current_password":"wrong-wrong-wrong","new_password":"newer horse battery 2"}`, hdr)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "invalid_credentials" {
		t.Fatalf("wrong current: %d %v", rec.Code, decodeMap(t, rec))
	}

	// New password below policy is refused.
	rec = do(t, h, http.MethodPost, "/api/v1/account/password",
		`{"current_password":"correct horse battery","new_password":"short"}`, hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak new: %d %v", rec.Code, decodeMap(t, rec))
	}

	// The real change: current session kept, other session ended.
	rec = do(t, h, http.MethodPost, "/api/v1/account/password",
		`{"current_password":"correct horse battery","new_password":"newer horse battery 2"}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("change = %d: %s", rec.Code, rec.Body.String())
	}
	if got := int(decodeMap(t, rec)["other_sessions_ended"].(float64)); got != 1 {
		t.Fatalf("other_sessions_ended = %d, want 1", got)
	}

	// The other device's session is gone...
	rec = do(t, h, http.MethodGet, "/api/v1/account/profile", "", otherHdr)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("other session survived = %d", rec.Code)
	}
	// ...the current one lives...
	rec = do(t, h, http.MethodGet, "/api/v1/account/profile", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("current session died = %d", rec.Code)
	}
	// ...the old password no longer works...
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old password still valid = %d", rec.Code)
	}
	// ...and the new one signs in.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"newer horse battery 2"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("new password login = %d: %s", rec.Code, rec.Body.String())
	}
}
