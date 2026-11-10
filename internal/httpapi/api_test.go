package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/metrics"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// newTestHandler builds a handler over a fresh temp store and also
// returns the store so tests can arrange state the way the installer CLI
// does (creating claim tokens, etc.).
func newTestHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(st, "test", []string{"/"}), st
}

// do performs a request against the handler and returns the recorder.
func do(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

func errCode(m map[string]any) string {
	s, _ := m["error"].(string)
	return s
}

func cookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

func TestHealthz(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := do(t, h, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q", body["status"])
	}
}

func TestReadyz(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := do(t, h, http.MethodGet, "/readyz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ready" {
		t.Fatalf("status field = %q", body["status"])
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := do(t, h, http.MethodPost, "/healthz", "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz status = %d, want 405", rec.Code)
	}
}

func TestUnknownAPIPathNotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := do(t, h, http.MethodGet, "/api/v1/unknown", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestMetricsRequiresFullSession pins the Sprint 2 security change: the
// metrics API is no longer anonymous.
func TestMetricsRequiresFullSession(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("metrics collectors read /proc, which only exists on Linux")
	}
	h, st := newTestHandler(t)

	// Anonymous → 401.
	rec := do(t, h, http.MethodGet, "/api/v1/server/metrics", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous metrics = %d, want 401", rec.Code)
	}

	// Full session → 200 with a real snapshot.
	token, _, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateClaimToken(context.Background(), auth.TokenHash(token), time.Hour); err != nil {
		t.Fatal(err)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		`{"token":"`+token+`","username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body.String())
	}
	sess := cookieOf(rec)
	sessHdr := map[string]string{"Cookie": sess.Name + "=" + sess.Value}
	// MFA enrolment (mandatory) upgrades the session to full scope (the
	// session cookie is unchanged — only its stored scope is upgraded).
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/setup", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa setup = %d: %s", rec.Code, rec.Body.String())
	}
	mfaSecret := decodeMap(t, rec)["secret"].(string)
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/verify",
		fmt.Sprintf(`{"code":%q}`, currentTOTP(t, mfaSecret)), sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa verify = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated metrics = %d: %s", rec.Code, rec.Body.String())
	}
	var snap metrics.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.CPU.Cores <= 0 || snap.Memory.TotalBytes <= 0 || len(snap.Disk) == 0 {
		t.Fatalf("implausible snapshot: %+v", snap)
	}
}

// TestOnboardingJourney drives the complete Sprint 2 happy path the way
// the browser wizard does: claim → account → MFA setup → verify →
// recovery codes → full-scope access.
func TestOnboardingJourney(t *testing.T) {
	h, st := newTestHandler(t)
	ctx := context.Background()

	// Installer CLI would have issued this token.
	token, _, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateClaimToken(ctx, auth.TokenHash(token), time.Hour); err != nil {
		t.Fatal(err)
	}

	// 1. Status is unclaimed and advertises an active claim token.
	rec := do(t, h, http.MethodGet, "/api/v1/onboarding/status", "", nil)
	m := decodeMap(t, rec)
	if m["instance_status"] != "unclaimed" || m["claim_token_active"] != true {
		t.Fatalf("initial status: %v", m)
	}

	// 2. Wrong token is rejected and the instance stays unclaimed.
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		`{"token":"drv1_wrong","username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "invalid_token" {
		t.Fatalf("wrong token: %d %v", rec.Code, decodeMap(t, rec))
	}

	// 3. Claim with the real token creates the admin and a session.
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		fmt.Sprintf(`{"token":%q,"username":"admin","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body.String())
	}
	sessCookie := cookieOf(rec)
	if sessCookie == nil {
		t.Fatal("claim did not set a session cookie")
	}
	if sessCookie.HttpOnly != true {
		t.Fatal("session cookie must be HttpOnly")
	}
	m = decodeMap(t, rec)
	if m["next"] != "mfa" {
		t.Fatalf("claim next = %v", m["next"])
	}
	sessHdr := map[string]string{"Cookie": sessCookie.Name + "=" + sessCookie.Value}

	// 3b. Token reuse is refused. Once the instance is claimed the
	// endpoint answers 409 wrong_state for everything (no token-validity
	// oracle after the first-owner claim).
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		fmt.Sprintf(`{"token":%q,"username":"second","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusConflict || errCode(decodeMap(t, rec)) != "wrong_state" {
		t.Fatalf("token reuse: %d %v", rec.Code, decodeMap(t, rec))
	}

	// 4. Full-scope APIs are blocked while onboarding is pending.
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", sessHdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("metrics during onboarding = %d, want 403", rec.Code)
	}

	// 5. MFA setup returns a secret and provisioning URI.
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/setup", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d: %s", rec.Code, rec.Body.String())
	}
	m = decodeMap(t, rec)
	secret, _ := m["secret"].(string)
	uri, _ := m["otpauth_uri"].(string)
	if secret == "" || !strings.HasPrefix(uri, "otpauth://totp/Drevanta:admin?") {
		t.Fatalf("setup payload: %v", m)
	}

	// 6. A wrong code is refused; the right code completes onboarding.
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/verify",
		`{"code":"000000"}`, sessHdr)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_invalid" {
		t.Fatalf("wrong code: %d %v", rec.Code, decodeMap(t, rec))
	}
	code := currentTOTP(t, secret)
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/verify",
		fmt.Sprintf(`{"code":%q}`, code), sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d: %s", rec.Code, rec.Body.String())
	}
	m = decodeMap(t, rec)
	codesAny, _ := m["recovery_codes"].([]any)
	if len(codesAny) != 10 {
		t.Fatalf("recovery codes = %d, want 10", len(codesAny))
	}
	var recoveryCodes []string
	for _, c := range codesAny {
		recoveryCodes = append(recoveryCodes, c.(string))
	}

	// 7. The session was upgraded to full scope: metrics work now.
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics after onboarding = %d: %s", rec.Code, rec.Body.String())
	}

	// 8. Session info reflects the completed state.
	rec = do(t, h, http.MethodGet, "/api/v1/auth/session", "", sessHdr)
	m = decodeMap(t, rec)
	if m["authenticated"] != true || m["scope"] != "full" || m["mfa_enabled"] != true {
		t.Fatalf("session info: %v", m)
	}

	// 9. Logout ends the session.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/logout", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/auth/session", "", sessHdr)
	m = decodeMap(t, rec)
	if m["authenticated"] != false {
		t.Fatalf("session survived logout: %v", m)
	}

	// 10. Login requires the second factor; recovery codes work when the
	// authenticator is unavailable.
	loginBody := `{"username":"admin","password":"correct horse battery"}`
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login", loginBody, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_required" {
		t.Fatalf("login without code: %d %v", rec.Code, decodeMap(t, rec))
	}
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery","totp":"000000"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_invalid" {
		t.Fatalf("login with wrong code: %d %v", rec.Code, decodeMap(t, rec))
	}
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":"admin","password":"correct horse battery","totp":%q}`, recoveryCodes[0]), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login with recovery code = %d: %s", rec.Code, rec.Body.String())
	}
	recoveryCookie := cookieOf(rec)
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "",
		map[string]string{"Cookie": recoveryCookie.Name + "=" + recoveryCookie.Value})
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics after recovery-code login = %d", rec.Code)
	}
	// The used recovery code cannot be replayed.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":"admin","password":"correct horse battery","totp":%q}`, recoveryCodes[0]), nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "recovery_invalid" {
		t.Fatalf("recovery replay: %d %v", rec.Code, decodeMap(t, rec))
	}
}

// The former TestOnboardingSkipRemoved (MFA mandatory, skip endpoint
// 404) was replaced by the optional-MFA suite in access_test.go:
// TestMFAOptionalSkipForNow, TestMFAEnrolledUsersKeepProtection and
// TestAccountMFAEnableLater.

func TestClaimStateGuards(t *testing.T) {
	h, st := newTestHandler(t)
	ctx := context.Background()

	token, _, _ := auth.NewToken()
	_, _ = st.CreateClaimToken(ctx, auth.TokenHash(token), time.Hour)

	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		fmt.Sprintf(`{"token":%q,"username":"admin","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d", rec.Code)
	}
	// A second (fresh, unconsumed) token cannot re-claim.
	token2, _, _ := auth.NewToken()
	_, _ = st.CreateClaimToken(ctx, auth.TokenHash(token2), time.Hour)
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		fmt.Sprintf(`{"token":%q,"username":"eve","password":"correct horse battery"}`, token2), nil)
	if rec.Code != http.StatusConflict || errCode(decodeMap(t, rec)) != "wrong_state" {
		t.Fatalf("re-claim: %d %v", rec.Code, decodeMap(t, rec))
	}
	// Login is refused while the instance is unclaimed is tested
	// elsewhere; here: MFA endpoints refuse an anonymous caller.
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/setup", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous mfa setup = %d, want 401", rec.Code)
	}
}

// TestClaimExpiredToken covers expiry handling on the claim endpoint.
func TestClaimExpiredToken(t *testing.T) {
	h, st := newTestHandler(t)
	token, _, _ := auth.NewToken()
	if _, err := st.CreateClaimToken(context.Background(), auth.TokenHash(token), -time.Minute); err != nil {
		t.Fatal(err)
	}
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		fmt.Sprintf(`{"token":%q,"username":"admin","password":"correct horse battery"}`, token), nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "token_expired" {
		t.Fatalf("expired token: %d %v", rec.Code, decodeMap(t, rec))
	}
}

// TestLoginValidation covers credential validation and the unclaimed state.
func TestLoginValidation(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusConflict || errCode(decodeMap(t, rec)) != "instance_unclaimed" {
		t.Fatalf("unclaimed login: %d %v", rec.Code, decodeMap(t, rec))
	}
}

// TestWeakCredentialsRejected pins server-side input validation.
func TestWeakCredentialsRejected(t *testing.T) {
	h, st := newTestHandler(t)
	token, _, _ := auth.NewToken()
	_, _ = st.CreateClaimToken(context.Background(), auth.TokenHash(token), time.Hour)
	cases := []struct {
		name, body string
	}{
		{"short password", fmt.Sprintf(`{"token":%q,"username":"admin","password":"short12"}`, token)},
		{"bad username chars", fmt.Sprintf(`{"token":%q,"username":"a b","password":"correct horse battery"}`, token)},
		{"short username", fmt.Sprintf(`{"token":%q,"username":"ab","password":"correct horse battery"}`, token)},
		{"empty token", `{"token":"","username":"admin","password":"correct horse battery"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim", tc.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestCrossOriginPostRejected covers the CSRF origin guard.
func TestCrossOriginPostRejected(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"a","password":"b"}`,
		map[string]string{"Origin": "http://evil.example"})
	if rec.Code != http.StatusForbidden || errCode(decodeMap(t, rec)) != "cross_origin" {
		t.Fatalf("cross-origin POST: %d %v", rec.Code, decodeMap(t, rec))
	}
	// Same-origin header passes (httptest requests default to
	// Host=example.com): the route proceeds to validation (409/400/401
	// territory, not 403).
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"a","password":"b"}`,
		map[string]string{"Origin": "http://example.com"})
	if rec.Code == http.StatusForbidden {
		t.Fatal("same-origin POST rejected")
	}
}

// TestRateLimiting pins the brute-force guard on unauthenticated endpoints.
func TestRateLimiting(t *testing.T) {
	h, _ := newTestHandler(t)
	body := `{"username":"nobody","password":"not-the-password"}`
	blocked := false
	for i := 0; i < 15; i++ {
		rec := do(t, h, http.MethodPost, "/api/v1/auth/login", body, nil)
		if rec.Code == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("login was never rate limited")
	}
}

// currentTOTP computes the current 6-digit TOTP code for a secret (test
// helper acting as the operator's authenticator app).
func currentTOTP(t *testing.T, secret string) string {
	t.Helper()
	code, err := auth.ComputeTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("totp: %v", err)
	}
	return code
}

// TestClaimTokenRegeneration pins the locked regeneration semantics end to
// end: rotating before the claim invalidates the previous token, the new
// token claims exactly once, and a consumed token is rejected on replay.
func TestClaimTokenRegeneration(t *testing.T) {
	h, st := newTestHandler(t)
	ctx := context.Background()

	first, _, _ := auth.NewToken()
	if _, err := st.CreateClaimToken(ctx, auth.TokenHash(first), time.Hour); err != nil {
		t.Fatal(err)
	}
	// Authorized regeneration BEFORE any claim: rotation revokes the first.
	second, _, _ := auth.NewToken()
	if _, err := st.CreateClaimToken(ctx, auth.TokenHash(second), time.Hour); err != nil {
		t.Fatal(err)
	}

	body := func(tok, user string) string {
		return fmt.Sprintf(`{"token":%q,"username":%q,"password":"correct horse battery"}`, tok, user)
	}

	// The rotated-out token is refused with the generic invalid_token reason.
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim", body(first, "attacker"), nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "invalid_token" {
		t.Fatalf("rotated-out token: %d %v", rec.Code, decodeMap(t, rec))
	}
	// The current token claims successfully.
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/claim", body(second, "admin"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim with rotated-in token = %d: %s", rec.Code, rec.Body.String())
	}
	// Replay of the now-consumed token is rejected (wrong state: claimed).
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/claim", body(second, "eve"), nil)
	if rec.Code != http.StatusConflict || errCode(decodeMap(t, rec)) != "wrong_state" {
		t.Fatalf("replay after claim: %d %v", rec.Code, decodeMap(t, rec))
	}
}
