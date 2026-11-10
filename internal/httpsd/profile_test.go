package httpsd

// Regression tests for ACME profile selection (Sprint 2.8 hotfix).
//
// The production incident this file guards against: Let's Encrypt's
// PRODUCTION directory advertises profiles under "meta.profiles" — not
// at the top level the way test CAs do. The original implementation
// only inspected the top level, silently dropped the requested
// "shortlived" profile, and the resulting default-profile order failed
// with "Default profile does not permit IP address identifiers."
// Successful Pebble issuance never caught it because the old code only
// sent a profile when the CA advertised one.
//
// These tests assert the actual bytes on the wire: the ACME new-order
// JWS payload must carry profile "shortlived" for an IP-identifier
// order, and issuance must FAIL CLOSED (zero orders sent) when the CA
// does not advertise the required profile.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// leProductionShape returns a directory document with the exact shape
// the Let's Encrypt PRODUCTION endpoint served when this bug was found
// (2026-11): no top-level profiles key, profiles under "meta" — with the
// endpoint URLs rewritten to point at the test server.
func leProductionShape(base string) string {
	return fmt.Sprintf(`{
		"keyChange": %q,
		"meta": {
			"caaIdentities": ["letsencrypt.org"],
			"profiles": {
				"classic": "https://letsencrypt.org/docs/profiles#classic",
				"shortlived": "https://letsencrypt.org/docs/profiles#shortlived",
				"tlsserver": "https://letsencrypt.org/docs/profiles#tlsserver"
			},
			"termsOfService": "https://letsencrypt.org/documents/le-sa",
			"website": "https://www.letsencrypt.org"
		},
		"newAccount": %q,
		"newNonce": %q,
		"newOrder": %q,
		"revokeCert": %q
	}`, base+"/key-change", base+"/sign-me-up", base+"/new-nonce",
		base+"/new-order", base+"/revoke-cert")
}

// draftShape advertises profiles at the top level (the shape the ACME
// profiles draft specifies and some CAs serve).
func draftShape(base string, profiles string) string {
	return fmt.Sprintf(`{
		"profiles": %s,
		"newAccount": %q,
		"newNonce": %q,
		"newOrder": %q,
		"revokeCert": %q
	}`, profiles, base+"/sign-me-up", base+"/new-nonce",
		base+"/new-order", base+"/revoke-cert")
}

// metaShape advertises profiles only under "meta" (the Let's Encrypt
// production shape) with a caller-chosen profile set.
func metaShape(base string, profiles string) string {
	return fmt.Sprintf(`{
		"meta": { "profiles": %s },
		"newAccount": %q,
		"newNonce": %q,
		"newOrder": %q,
		"revokeCert": %q
	}`, profiles, base+"/sign-me-up", base+"/new-nonce",
		base+"/new-order", base+"/revoke-cert")
}

const classicOnly = `{"classic": "https://letsencrypt.org/docs/profiles#classic"}`

// capturedOrder is the decoded payload of an ACME new-order request.
type capturedOrder struct {
	Identifiers []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"identifiers"`
	Profile string `json:"profile"`
}

// acmeMock is a minimal ACME server: enough for lego to register an
// account and place an order, capturing every new-order payload. The
// order itself is always rejected (400) — these tests assert the
// request, not the issuance. The server is TLS: lego refuses plain-HTTP
// ACME directory URLs, so the tests point the manager's acme_ca_root at
// the mock's own certificate.
type acmeMock struct {
	server *httptest.Server
	caRoot string // PEM path the ACME client must trust

	mu     sync.Mutex
	dirDoc string
	orders []capturedOrder
}

func newACMEMock(t *testing.T, dirDoc string) *acmeMock {
	t.Helper()
	m := &acmeMock{dirDoc: dirDoc}
	mux := http.NewServeMux()
	mux.HandleFunc("/directory", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(m.dirDoc))
	})
	mux.HandleFunc("/new-nonce", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "test-nonce")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/sign-me-up", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", m.server.URL+"/my-account/1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"valid"}`))
	})
	mux.HandleFunc("/new-order", func(w http.ResponseWriter, r *http.Request) {
		var env struct {
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&env); err == nil && env.Payload != "" {
			if raw, derr := base64.RawURLEncoding.DecodeString(env.Payload); derr == nil {
				var order capturedOrder
				if uerr := json.Unmarshal(raw, &order); uerr == nil {
					m.mu.Lock()
					m.orders = append(m.orders, order)
					m.mu.Unlock()
				}
			}
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"urn:ietf:params:acme:error:rejectedIdentifier","detail":"captured; aborting test order"}`))
	})
	m.server = httptest.NewTLSServer(mux)
	m.dirDoc = strings.ReplaceAll(m.dirDoc, "BASE_URL", m.server.URL)
	// Export the mock's listener certificate so the ACME client can
	// trust it via the Config.CARoot path.
	certPath := t.TempDir() + "/mock-acme-cert.pem"
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	m.caRoot = certPath
	t.Cleanup(m.server.Close)
	return m
}

func (m *acmeMock) orderCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.orders)
}

func (m *acmeMock) lastOrder() capturedOrder {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.orders) == 0 {
		return capturedOrder{}
	}
	return m.orders[len(m.orders)-1]
}

// profileManager builds a Manager pointed at the mock (TLS with the
// mock's certificate trusted via CARoot).
func profileManager(t *testing.T, dirURL, host, caRoot string) *Manager {
	t.Helper()
	cfg := Config{
		Enabled:   true,
		Host:      host,
		Backend:   "127.0.0.1:1", // never reached: the mock rejects every order
		Profile:   DefaultProfile,
		ACMEDir:   dirURL,
		CARoot:    caRoot,
		HTTPPort:  testHTTPPort,
		HTTPSPort: testTLSPort,
	}
	return NewManager(cfg, t.TempDir())
}

// TestIssueOnce_SendsShortlivedProfileForIPHost is THE production
// regression: a Let's Encrypt-shaped directory (profiles under "meta")
// must produce a new-order request that carries profile "shortlived"
// for an IP identifier. The pre-fix code sent no profile at all and the
// CA answered "Default profile does not permit IP address identifiers."
func TestIssueOnce_SendsShortlivedProfileForIPHost(t *testing.T) {
	mock := newACMEMock(t, leProductionShape("BASE_URL"))
	mgr := profileManager(t, mock.server.URL+"/directory", "203.0.113.7", mock.caRoot)

	// The mock rejects the order after capture, so IssueOnce must return
	// an error — the assertions below are about the request bytes.
	_ = mgr.IssueOnce(context.Background())

	if got := mock.orderCount(); got != 1 {
		t.Fatalf("new-order requests captured = %d, want 1", got)
	}
	order := mock.lastOrder()
	if len(order.Identifiers) != 1 {
		t.Fatalf("identifiers = %+v, want exactly one", order.Identifiers)
	}
	if order.Identifiers[0].Type != "ip" || order.Identifiers[0].Value != "203.0.113.7" {
		t.Fatalf("identifier = %+v, want ip 203.0.113.7 (RFC 8738)", order.Identifiers[0])
	}
	if order.Profile != "shortlived" {
		t.Fatalf("new-order profile = %q, want %q — this is the production regression (CA answered: Default profile does not permit IP address identifiers)", order.Profile, DefaultProfile)
	}
}

// TestIssueOnce_IPHostFailsClosedWhenProfileNotAdvertised: a CA that
// advertises other profiles but not "shortlived" must never see an
// IP-identifier order — issuance aborts before any ACME order request.
func TestIssueOnce_IPHostFailsClosedWhenProfileNotAdvertised(t *testing.T) {
	cases := []struct {
		name   string
		dirDoc func() string
	}{
		{"meta shape, classic only", func() string { return metaShape("BASE_URL", classicOnly) }},
		{"draft shape, classic only", func() string { return draftShape("BASE_URL", classicOnly) }},
		{"no profiles advertised at all", func() string {
			return `{"newAccount": "BASE_URL/sign-me-up", "newNonce": "BASE_URL/new-nonce", "newOrder": "BASE_URL/new-order", "revokeCert": "BASE_URL/revoke-cert"}`
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newACMEMock(t, tc.dirDoc())
			mgr := profileManager(t, mock.server.URL+"/directory", "203.0.113.7", mock.caRoot)

			err := mgr.IssueOnce(context.Background())
			if err == nil {
				t.Fatal("issuance succeeded without the required profile — must fail closed")
			}
			if !strings.Contains(err.Error(), DefaultProfile) || !strings.Contains(err.Error(), "refusing") {
				t.Fatalf("error does not clearly name the missing profile: %v", err)
			}
			if got := mock.orderCount(); got != 0 {
				t.Fatalf("new-order requests sent = %d, want 0 (fail closed)", got)
			}
		})
	}
}

// TestIssueOnce_DNSHostFallsBackToDefault: for a DNS-name subject the
// CA default profile is acceptable — the order goes out without a
// profile when the CA does not advertise the requested one.
func TestIssueOnce_DNSHostFallsBackToDefault(t *testing.T) {
	mock := newACMEMock(t, metaShape("BASE_URL", classicOnly))
	mgr := profileManager(t, mock.server.URL+"/directory", "dashboard.example.com", mock.caRoot)

	_ = mgr.IssueOnce(context.Background())

	if got := mock.orderCount(); got != 1 {
		t.Fatalf("new-order requests captured = %d, want 1", got)
	}
	order := mock.lastOrder()
	if len(order.Identifiers) != 1 || order.Identifiers[0].Type != "dns" ||
		order.Identifiers[0].Value != "dashboard.example.com" {
		t.Fatalf("identifier = %+v, want dns dashboard.example.com", order.Identifiers)
	}
	if order.Profile != "" {
		t.Fatalf("new-order profile = %q, want empty (CA default) for a DNS order", order.Profile)
	}
}

// TestResolveProfile_Table pins the advertisement parsing and the
// fail-closed decision for both directory shapes, including unreachable
// and non-JSON directories.
func TestResolveProfile_Table(t *testing.T) {
	cases := []struct {
		name     string
		dirDoc   func() string
		host     string
		useMock  bool // false = unreachable directory URL
		wantProf string
		wantErr  bool
	}{
		{"LE production shape + IP", func() string { return leProductionShape("BASE_URL") }, "203.0.113.7", true, "shortlived", false},
		{"LE production shape + DNS", func() string { return leProductionShape("BASE_URL") }, "dashboard.example.com", true, "shortlived", false},
		{"draft shape + IP", func() string { return draftShape("BASE_URL", `{"shortlived": "x"}`) }, "203.0.113.7", true, "shortlived", false},
		{"meta classic-only + IP", func() string { return metaShape("BASE_URL", classicOnly) }, "203.0.113.7", true, "", true},
		{"draft classic-only + IP", func() string { return draftShape("BASE_URL", classicOnly) }, "203.0.113.7", true, "", true},
		{"no profiles + IP", func() string { return `{"meta": {}}` }, "203.0.113.7", true, "", true},
		{"not a JSON directory + IP", func() string { return "<html>proxy error</html>" }, "203.0.113.7", true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dirURL := "http://127.0.0.1:1/directory" // closed port: unreachable
			caRoot := ""
			if tc.useMock {
				mock := newACMEMock(t, tc.dirDoc())
				dirURL = mock.server.URL + "/directory"
				caRoot = mock.caRoot
			}
			mgr := profileManager(t, dirURL, tc.host, caRoot)
			prof, err := mgr.resolveProfile(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveProfile = (%q, nil), want error", prof)
				}
				if !strings.Contains(err.Error(), "refusing") {
					t.Fatalf("error does not state the fail-closed decision: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveProfile error: %v", err)
			}
			if prof != tc.wantProf {
				t.Fatalf("resolveProfile = %q, want %q", prof, tc.wantProf)
			}
		})
	}
}
