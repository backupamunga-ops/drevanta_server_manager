package httpapi

// v0.4.17: recovery-code sign-in, email reset mechanics, SMTP settings.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
)

func TestRecoveryLoginFlow(t *testing.T) {
	h, st := newTestHandler(t)
	_, recovery := enrollAdminMFA(t, h, st)

	// Wrong code: constant invalid_credentials.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/recovery-login",
		`{"username":"admin","code":"not-a-code"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "invalid_credentials" {
		t.Fatalf("wrong code: %d %v", rec.Code, decodeMap(t, rec))
	}

	// Correct code: limited session that may only set a password.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/recovery-login",
		fmt.Sprintf(`{"username":"admin","code":%q}`, recovery[0]), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery login = %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeMap(t, rec)
	if m["scope"] != "recovery" || m["next"] != "password" {
		t.Fatalf("recovery login payload: %v", m)
	}
	sess := cookieOf(rec)
	hdr := map[string]string{"Cookie": sess.Name + "=" + sess.Value}

	// The limited session cannot read metrics...
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", hdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("recovery session metrics = %d, want 403", rec.Code)
	}
	// ...cannot read the profile...
	rec = do(t, h, http.MethodGet, "/api/v1/account/profile", "", hdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("recovery session profile = %d, want 403", rec.Code)
	}
	// ...but can set a new password (no current password needed).
	rec = do(t, h, http.MethodPost, "/api/v1/account/password",
		`{"new_password":"fresh horse battery 7"}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery password set = %d: %s", rec.Code, rec.Body.String())
	}

	// The session is now full scope.
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("upgraded session metrics = %d", rec.Code)
	}

	// The old password is gone; the new one signs in.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery","totp":"000000"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old password still valid = %d", rec.Code)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"fresh horse battery 7"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_required" {
		t.Fatalf("new password login: %d %v", rec.Code, decodeMap(t, rec))
	}

	// The consumed recovery code cannot be replayed.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/recovery-login",
		fmt.Sprintf(`{"username":"admin","code":%q}`, recovery[0]), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("recovery code replay = %d", rec.Code)
	}
}

func TestForgotPasswordAlwaysGeneric(t *testing.T) {
	h, st := newTestHandler(t)
	claimAndSkip(t, h, st, "op@example.com")

	// Unknown identifier: same generic ok.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/forgot", `{"username":"ghost@x.com"}`, nil)
	if rec.Code != http.StatusOK || decodeMap(t, rec)["ok"] != true {
		t.Fatalf("forgot unknown: %d %v", rec.Code, decodeMap(t, rec))
	}
	// Known identifier but no SMTP configured: same generic ok.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/forgot", `{"username":"admin"}`, nil)
	if rec.Code != http.StatusOK || decodeMap(t, rec)["ok"] != true {
		t.Fatalf("forgot without smtp: %d %v", rec.Code, decodeMap(t, rec))
	}
	// No reset token was created.
	_, err := st.ConsumePasswordReset(t.Context(), "anything")
	if err == nil {
		t.Fatal("no tokens should exist")
	}
}

func TestPasswordResetEndpoint(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAndSkip(t, h, st, "op@example.com")

	// A "second device" session that must die with the reset.
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	other := cookieOf(rec)
	otherHdr := map[string]string{"Cookie": other.Name + "=" + other.Value}

	token, _, _ := auth.NewToken()
	if err := st.CreatePasswordReset(t.Context(), auth.TokenHash(token), 1, 30*time.Minute); err != nil {
		t.Fatal(err)
	}

	// Weak new password refused.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/reset",
		fmt.Sprintf(`{"token":%q,"new_password":"short"}`, token), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak reset = %d", rec.Code)
	}

	// The real reset.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/reset",
		fmt.Sprintf(`{"token":%q,"new_password":"reset horse battery 3"}`, token), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset = %d: %s", rec.Code, rec.Body.String())
	}

	// The token is spent.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/reset",
		fmt.Sprintf(`{"token":%q,"new_password":"reset horse battery 3"}`, token), nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "reset_invalid" {
		t.Fatalf("token replay: %d %v", rec.Code, decodeMap(t, rec))
	}

	// Every pre-existing session is dead.
	for name, h2 := range map[string]map[string]string{"first": hdr, "other": otherHdr} {
		rec = do(t, h, http.MethodGet, "/api/v1/account/profile", "", h2)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s session survived reset = %d", name, rec.Code)
		}
	}

	// The new password signs in.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"reset horse battery 3"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login after reset = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSMTPSettingsRoundtrip(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAndSkip(t, h, st, "op@example.com")

	rec := do(t, h, http.MethodGet, "/api/v1/account/smtp", "", hdr)
	if rec.Code != http.StatusOK || decodeMap(t, rec)["enabled"] != false {
		t.Fatalf("initial smtp: %d %v", rec.Code, decodeMap(t, rec))
	}

	// Save with a username but no stored password yet -> refused.
	rec = do(t, h, http.MethodPost, "/api/v1/account/smtp",
		`{"enabled":true,"host":"smtp.example.com","port":587,"username":"user","from_addr":"drevanta@example.com"}`, hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing password = %d %v", rec.Code, decodeMap(t, rec))
	}

	// Full save.
	rec = do(t, h, http.MethodPost, "/api/v1/account/smtp",
		`{"enabled":true,"host":"smtp.example.com","port":587,"username":"user","password":"smtp-secret","from_addr":"drevanta@example.com"}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("smtp set = %d: %s", rec.Code, rec.Body.String())
	}

	// Read back: never the password itself.
	rec = do(t, h, http.MethodGet, "/api/v1/account/smtp", "", hdr)
	m := decodeMap(t, rec)
	if m["enabled"] != true || m["host"] != "smtp.example.com" || m["password_set"] != true {
		t.Fatalf("smtp get: %v", m)
	}
	if _, present := m["password"]; present {
		t.Fatal("the SMTP password must never be returned")
	}

	// Update without password keeps the stored one.
	rec = do(t, h, http.MethodPost, "/api/v1/account/smtp",
		`{"enabled":true,"host":"smtp2.example.com","port":465,"username":"user","from_addr":"drevanta@example.com"}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("smtp update = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/account/smtp", "", hdr)
	if decodeMap(t, rec)["password_set"] != true {
		t.Fatal("password was lost on update")
	}

	// The test-send fails against a non-existent relay but must return a
	// structured ok:false, not a 5xx.
	rec = do(t, h, http.MethodPost, "/api/v1/account/smtp/test", `{}`, hdr)
	if rec.Code != http.StatusOK || decodeMap(t, rec)["ok"] != false {
		t.Fatalf("smtp test = %d %v", rec.Code, decodeMap(t, rec))
	}
}
