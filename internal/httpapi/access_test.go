// Tests for the optional-MFA ("Skip for now"), Account-Security
// enable-later, trusted access-status, and fail-safe custom-domain
// endpoints.
package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpsd"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// claimAsAdmin walks the claim step and returns the onboarding session
// cookie header.
func claimAsAdmin(t *testing.T, h http.Handler, st *store.Store, username string) map[string]string {
	t.Helper()
	ctx := context.Background()
	token, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := st.CreateClaimToken(ctx, auth.TokenHash(token), time.Hour); err != nil {
		t.Fatalf("claim token: %v", err)
	}
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/claim",
		fmt.Sprintf(`{"token":%q,"username":%q,"email":"admin@example.com","password":"correct horse battery"}`, token, username), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body.String())
	}
	c := cookieOf(rec)
	if c == nil {
		t.Fatal("claim produced no session cookie")
	}
	return map[string]string{"Cookie": c.Name + "=" + c.Value}
}

// TestMFAOptionalSkipForNow replaces the old "skip removed" pin: MFA is
// optional with an EXPLICIT acknowledgement. Skipping completes
// onboarding, upgrades the session to full scope, is audited, and
// leaves password-only logins working.
func TestMFAOptionalSkipForNow(t *testing.T) {
	h, st := newTestHandler(t)
	sessHdr := claimAsAdmin(t, h, st, "admin")

	// Without the explicit acknowledgement the skip is refused and the
	// instance stays mfa_pending.
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{}`, sessHdr)
	if rec.Code != http.StatusBadRequest || errCode(decodeMap(t, rec)) != "acknowledgment_required" {
		t.Fatalf("unacknowledged skip = %d %s", rec.Code, rec.Body.String())
	}
	state, _ := st.InstanceState(context.Background())
	if state.Status != store.StatusMFAPending {
		t.Fatalf("status after refused skip = %s, want mfa_pending", state.Status)
	}

	// With the acknowledgement the skip completes onboarding.
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{"acknowledge_risk":true}`, sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeMap(t, rec)
	if body["instance_status"] != "active" || body["mfa_mode"] != "skipped" {
		t.Fatalf("skip body = %v", body)
	}

	// The same session is now full-scope: the dashboard API answers.
	rec = do(t, h, http.MethodGet, "/api/v1/server/metrics", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics with post-skip session = %d: %s", rec.Code, rec.Body.String())
	}

	// The skip is audited with the acknowledgement.
	events, err := st.RecentAuditEvents(context.Background(), "mfa_skipped", 5)
	if err != nil || len(events) == 0 {
		t.Fatalf("mfa_skipped audit events = %v, %v", events, err)
	}
	if !strings.Contains(events[0].Detail, `"acknowledged":true`) {
		t.Fatalf("audit detail = %s, want acknowledged:true", events[0].Detail)
	}

	// A skipped administrator signs in with password only and gets a
	// full-scope session (the dashboard is reachable).
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login after skip = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeMap(t, rec)["scope"]; got != "full" {
		t.Fatalf("login-after-skip scope = %v, want full", got)
	}
}

// TestMFAEnrolledUsersKeepProtection pins that the optional-skip change
// did not weaken enrolled accounts: they still need TOTP at login.
func TestMFAEnrolledUsersKeepProtection(t *testing.T) {
	h, st := newTestHandler(t)
	sessHdr := claimAsAdmin(t, h, st, "admin")

	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/setup", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup = %d: %s", rec.Code, rec.Body.String())
	}
	secret := decodeMap(t, rec)["secret"].(string)
	rec = do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/verify",
		fmt.Sprintf(`{"code":%q}`, currentTOTP(t, secret)), sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d: %s", rec.Code, rec.Body.String())
	}

	// Password-only login now fails; the code requirement stays.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_required" {
		t.Fatalf("enrolled password-only login = %d %s, want totp_required", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":"admin","password":"correct horse battery","totp":%q}`, currentTOTP(t, secret)), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enrolled TOTP login = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAccountMFAEnableLater covers the Account-Security path: an
// administrator who skipped during onboarding can enable MFA later;
// recovery codes are REQUIRED (a fresh set is issued) and enrolled
// accounts cannot re-run setup.
func TestAccountMFAEnableLater(t *testing.T) {
	h, st := newTestHandler(t)
	sessHdr := claimAsAdmin(t, h, st, "admin")

	// Skip during onboarding.
	rec := do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{"acknowledge_risk":true}`, sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}

	// Posture: disabled.
	rec = do(t, h, http.MethodGet, "/api/v1/account/mfa", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("account mfa status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeMap(t, rec)["mfa_enabled"]; got != false {
		t.Fatalf("mfa_enabled = %v", got)
	}

	// Verify before setup is refused.
	rec = do(t, h, http.MethodPost, "/api/v1/account/mfa/verify", `{"code":"123456"}`, sessHdr)
	if rec.Code != http.StatusBadRequest || errCode(decodeMap(t, rec)) != "mfa_not_setup" {
		t.Fatalf("verify-without-setup = %d %s", rec.Code, rec.Body.String())
	}

	// Setup + verify with a real TOTP code.
	rec = do(t, h, http.MethodPost, "/api/v1/account/mfa/setup", "", sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("account setup = %d: %s", rec.Code, rec.Body.String())
	}
	secret := decodeMap(t, rec)["secret"].(string)
	rec = do(t, h, http.MethodPost, "/api/v1/account/mfa/verify",
		fmt.Sprintf(`{"code":%q}`, currentTOTP(t, secret)), sessHdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("account verify = %d: %s", rec.Code, rec.Body.String())
	}
	codes := decodeMap(t, rec)["recovery_codes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("recovery codes = %d, want 10", len(codes))
	}

	// Posture: enabled, 10 unused codes.
	rec = do(t, h, http.MethodGet, "/api/v1/account/mfa", "", sessHdr)
	body := decodeMap(t, rec)
	if body["mfa_enabled"] != true || body["recovery_codes_unused"].(float64) != 10 {
		t.Fatalf("posture = %v", body)
	}

	// Setup again is refused.
	rec = do(t, h, http.MethodPost, "/api/v1/account/mfa/setup", "", sessHdr)
	if rec.Code != http.StatusConflict || errCode(decodeMap(t, rec)) != "mfa_already_enabled" {
		t.Fatalf("re-setup = %d %s, want mfa_already_enabled", rec.Code, rec.Body.String())
	}

	// Login now requires the second factor.
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		`{"username":"admin","password":"correct horse battery"}`, nil)
	if rec.Code != http.StatusUnauthorized || errCode(decodeMap(t, rec)) != "totp_required" {
		t.Fatalf("post-enable password-only login = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":"admin","password":"correct horse battery","totp":%q}`, currentTOTP(t, secret)), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-enable TOTP login = %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Access status + custom-domain fail-safe flow
// ---------------------------------------------------------------------------

type accessRig struct {
	h          http.Handler
	st         *store.Store
	view       *AccessView
	cfgDir     string
	stateDir   string
	overlayDir string
}

func newAccessRig(t *testing.T) *accessRig {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	root := t.TempDir()
	rig := &accessRig{
		st:         st,
		cfgDir:     root,
		stateDir:   filepath.Join(root, "state"),
		overlayDir: filepath.Join(root, "overlay"),
	}
	rig.view = &AccessView{
		ConfigPath:    filepath.Join(rig.cfgDir, "https.json"),
		HTTPSStateDir: rig.stateDir,
		OverlayDir:    rig.overlayDir,
		Now:           func() time.Time { return time.Now() },
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.7")}, nil
		},
		PublicIPs:   func() []string { return []string{"203.0.113.7"} },
		VerifyRoute: func(context.Context, string, int) error { return nil },
		WaitTimeout: 2 * time.Second,
		PollEvery:   20 * time.Millisecond,
	}
	rig.h = NewWithAccess(st, "test", []string{"/"}, rig.view)
	return rig
}

func (rig *accessRig) enableHTTPS(t *testing.T, host string) {
	t.Helper()
	cfg := httpsd.Default()
	cfg.Host = host
	if err := cfg.Save(rig.view.ConfigPath); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func (rig *accessRig) seedActivePrimary(t *testing.T) {
	t.Helper()
	st := httpsd.Status{
		Phase:     httpsd.PhaseActive,
		Host:      "203.0.113.7",
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
	}
	if err := httpsd.WriteStatus(rig.stateDir, st); err != nil {
		t.Fatalf("write status: %v", err)
	}
}

func (rig *accessRig) seedActiveDomain(t *testing.T, domain string) {
	t.Helper()
	st, err := httpsd.ReadStatus(rig.stateDir)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	st.Domain = &httpsd.DomainStatus{
		Host:      domain,
		Phase:     httpsd.PhaseActive,
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
	}
	if err := httpsd.WriteStatus(rig.stateDir, st); err != nil {
		t.Fatalf("write status: %v", err)
	}
}

func TestAccessStatusFromTrustedConfig(t *testing.T) {
	rig := newAccessRig(t)

	// Nothing configured: private mode.
	rec := do(t, rig.h, http.MethodGet, "/api/v1/access/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeMap(t, rec)
	if body["mode"] != "private" {
		t.Fatalf("initial mode = %v, want private", body["mode"])
	}

	// Enabled configuration with a valid certificate: https mode,
	// sourced from the trusted files (no auth, no secrets).
	rig.enableHTTPS(t, "203.0.113.7")
	rig.seedActivePrimary(t)
	rec = do(t, rig.h, http.MethodGet, "/api/v1/access/status", "", nil)
	body = decodeMap(t, rec)
	if body["mode"] != "https" {
		t.Fatalf("mode = %v, want https", body["mode"])
	}
	hs := body["https"].(map[string]any)
	if hs["host"] != "203.0.113.7" || hs["cert_valid"] != true {
		t.Fatalf("https block = %v", hs)
	}
}

func TestDomainCheckRejectsWrongDNS(t *testing.T) {
	rig := newAccessRig(t)
	rig.enableHTTPS(t, "203.0.113.7")
	rig.seedActivePrimary(t)
	hdr := claimAsAdmin(t, rig.h, rig.st, "admin")
	if rec := do(t, rig.h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{"acknowledge_risk":true}`, hdr); rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}

	// DNS resolves to a foreign address: check reports the mismatch.
	rig.view.LookupIP = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("198.51.100.99")}, nil
	}
	rec := do(t, rig.h, http.MethodPost, "/api/v1/access/domain/check", `{"domain":"vpn.example.com"}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("check = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeMap(t, rec)["matches"]; got != false {
		t.Fatalf("matches = %v, want false", got)
	}

	// Apply refuses to touch anything on a DNS mismatch.
	rec = do(t, rig.h, http.MethodPost, "/api/v1/access/domain/apply", `{"domain":"vpn.example.com"}`, hdr)
	if rec.Code != http.StatusUnprocessableEntity || errCode(decodeMap(t, rec)) != "dns_mismatch" {
		t.Fatalf("apply with wrong DNS = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(rig.overlayDir, "domain.json")); !os.IsNotExist(err) {
		t.Fatal("overlay must not exist after a rejected apply")
	}
}

func TestDomainApplyHappyPathAndFailSafe(t *testing.T) {
	rig := newAccessRig(t)
	rig.enableHTTPS(t, "203.0.113.7")
	rig.seedActivePrimary(t)
	hdr := claimAsAdmin(t, rig.h, rig.st, "admin")
	if rec := do(t, rig.h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{"acknowledge_risk":true}`, hdr); rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}

	// Happy path: DNS ok → overlay written → domain slot active (the
	// proxy is faked by seeding the status) → route verified.
	rig.seedActiveDomain(t, "vpn.example.com")
	rec := do(t, rig.h, http.MethodPost, "/api/v1/access/domain/apply", `{"domain":"vpn.example.com"}`, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d: %s", rec.Code, rec.Body.String())
	}
	ov, err := httpsd.LoadDomainOverlay(rig.overlayDir)
	if err != nil || ov.Domain != "vpn.example.com" {
		t.Fatalf("overlay after apply = (%q, %v)", ov.Domain, err)
	}

	// Route verification failure AFTER the certificate went active must
	// roll the overlay back: no domain, IP access untouched.
	rig.view.VerifyRoute = func(context.Context, string, int) error {
		return fmt.Errorf("TLS handshake failed: certificate is not valid for vpn.example.com")
	}
	rec = do(t, rig.h, http.MethodPost, "/api/v1/access/domain/apply", `{"domain":"vpn.example.com"}`, hdr)
	if rec.Code != http.StatusBadGateway || errCode(decodeMap(t, rec)) != "domain_not_ready" {
		t.Fatalf("apply with broken route = %d %s", rec.Code, rec.Body.String())
	}
	ov, err = httpsd.LoadDomainOverlay(rig.overlayDir)
	if err != nil || ov.Domain != "" {
		t.Fatalf("overlay must be cleared after a failed apply: (%q, %v)", ov.Domain, err)
	}

	// The primary slot status never changed.
	st, _ := httpsd.ReadStatus(rig.stateDir)
	if st.Phase != httpsd.PhaseActive || st.Host != "203.0.113.7" {
		t.Fatalf("primary status = %+v, want active 203.0.113.7", st)
	}

	// Remove requires the explicit confirm flag.
	rec = do(t, rig.h, http.MethodPost, "/api/v1/access/domain/remove", `{}`, hdr)
	if rec.Code != http.StatusBadRequest || errCode(decodeMap(t, rec)) != "confirm_required" {
		t.Fatalf("remove without confirm = %d %s", rec.Code, rec.Body.String())
	}

	// Audit trail exists for applied/failed domain operations.
	for _, action := range []string{"domain_applied", "domain_apply_failed"} {
		events, aerr := rig.st.RecentAuditEvents(context.Background(), action, 5)
		if aerr != nil || len(events) == 0 {
			t.Fatalf("audit %s: %d events, err %v", action, len(events), aerr)
		}
	}
}

func TestDomainApplyWithoutHTTPSConflicts(t *testing.T) {
	rig := newAccessRig(t) // no https.json: HTTPS disabled
	hdr := claimAsAdmin(t, rig.h, rig.st, "admin")
	if rec := do(t, rig.h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{"acknowledge_risk":true}`, hdr); rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}
	rec := do(t, rig.h, http.MethodPost, "/api/v1/access/domain/check", `{"domain":"vpn.example.com"}`, hdr)
	if rec.Code != http.StatusConflict || errCode(decodeMap(t, rec)) != "https_disabled" {
		t.Fatalf("check without HTTPS = %d %s", rec.Code, rec.Body.String())
	}

	// Invalid hostnames are refused before anything else.
	rig.enableHTTPS(t, "203.0.113.7")
	rig.seedActivePrimary(t)
	rec = do(t, rig.h, http.MethodPost, "/api/v1/access/domain/check", `{"domain":"not a domain"}`, hdr)
	if rec.Code != http.StatusBadRequest || errCode(decodeMap(t, rec)) != "invalid_domain" {
		t.Fatalf("invalid domain = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, rig.h, http.MethodPost, "/api/v1/access/domain/check", `{"domain":"203.0.113.7"}`, hdr)
	if rec.Code != http.StatusBadRequest || errCode(decodeMap(t, rec)) != "invalid_domain" {
		t.Fatalf("IP as domain = %d %s", rec.Code, rec.Body.String())
	}
}

// TestAccessEndpointsUnavailableWithoutView pins the 503 behaviour of
// identity-only rigs (nil AccessView): the endpoints answer honestly
// instead of guessing.
func TestAccessEndpointsUnavailableWithoutView(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAsAdmin(t, h, st, "admin")
	if rec := do(t, h, http.MethodPost, "/api/v1/onboarding/mfa/skip", `{"acknowledge_risk":true}`, hdr); rec.Code != http.StatusOK {
		t.Fatalf("skip = %d: %s", rec.Code, rec.Body.String())
	}
	rec := do(t, h, http.MethodPost, "/api/v1/access/domain/check", `{"domain":"vpn.example.com"}`, hdr)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("check without view = %d, want 503", rec.Code)
	}
}
