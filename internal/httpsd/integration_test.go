package httpsd

// End-to-end ACME integration test against pebble (the Let's Encrypt
// test ACME server). It exercises the real issuance path: account
// registration, RFC 8738 IP identifier order, http-01 + tls-alpn-01
// challenges answered by the proxy's own listeners, certificate swap-in
// and one renewal — plus the proxy behaviour rules.
//
// Skipped unless DREVANTA_PEBBLE_BIN points at a pebble binary; the CI
// "acme" job downloads one. Run locally with:
//
//      DREVANTA_PEBBLE_BIN=/path/to/pebble go test ./internal/httpsd -run TestPebble -v

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	pebbleACME   = "127.0.0.1:14000"
	pebbleMgmt   = "127.0.0.1:15000"
	testHTTPPort = 18081 // pebble validates http-01 here
	testTLSPort  = 18443 // pebble validates tls-alpn-01 here
)

func pebbleRootCAPool(t *testing.T) *x509.CertPool {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // fetching the root that defines trust
	}}
	resp, err := client.Get("https://" + pebbleMgmt + "/roots/0")
	if err != nil {
		t.Fatalf("fetch pebble root: %v", err)
	}
	defer resp.Body.Close()
	pemBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read pebble root: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemBytes)
	if len(pool.Subjects()) == 0 {
		t.Fatal("pebble root fetch produced an empty pool")
	}
	return pool
}

func startPebble(t *testing.T, dir string) {
	t.Helper()
	bin := os.Getenv("DREVANTA_PEBBLE_BIN")
	if bin == "" {
		t.Skip("DREVANTA_PEBBLE_BIN not set")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("pebble binary not usable: %v", err)
	}

	// Certificate for pebble's own TLS endpoints (ACME + management).
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("listener-cert.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	write("listener-key.pem", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	cfg := fmt.Sprintf(`{
          "pebble": {
            "listenAddress": %q,
            "managementListenAddress": %q,
            "certificate": %q,
            "privateKey": %q,
            "httpPort": %d,
            "tlsPort": %d,
            "externalAccountBindingRequired": false,
            "domainBlocklist": [],
            "profiles": {
              "default":  {"description": "default", "validityPeriod": 7776000},
              "shortlived": {"description": "short-lived IP certs", "validityPeriod": 518400}
            }
          }
        }`, pebbleACME, pebbleMgmt,
		filepath.Join(dir, "listener-cert.pem"), filepath.Join(dir, "listener-key.pem"),
		testHTTPPort, testTLSPort)
	cfgPath := filepath.Join(dir, "pebble-config.json")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-config", cfgPath)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pebble: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	// Wait for the ACME directory to answer.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}}
		resp, err := client.Get("https://" + pebbleACME + "/dir")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("pebble did not come up within 15s")
}

// integrationFixture wires pebble + proxy + a stub loopback backend.
type integrationFixture struct {
	t           *testing.T
	stateDir    string
	cfg         Config
	mgr         *Manager
	backend     *http.Server
	backendSeen http.Header
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	root := t.TempDir()
	startPebble(t, root)
	stateDir := filepath.Join(root, "state")
	f := &integrationFixture{
		t:        t,
		stateDir: stateDir,
		cfg: Config{
			Enabled:     true,
			Host:        "127.0.0.1",
			HTTPPort:    testHTTPPort,
			HTTPSPort:   testTLSPort,
			Backend:     "127.0.0.1:19090",
			Profile:     DefaultProfile, // pebble advertises it (see startPebble); the fixed resolveProfile sends it
			ACMEDir:     "https://" + pebbleACME + "/dir",
			CARoot:      "", // trust via the pool client below is per-request; lego uses CARoot only
			HTTP01Port:  testHTTPPort,
			TLSALPNPort: testTLSPort,
		},
	}
	f.mgr = NewManager(f.cfg, stateDir)

	// Stub backend recording the headers the proxy forwarded.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.backendSeen = r.Header.Clone()
		_, _ = w.Write([]byte("backend-ok"))
	})
	f.backend = &http.Server{Addr: f.cfg.Backend, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", f.cfg.Backend)
	if err != nil {
		t.Fatalf("stub backend listen: %v", err)
	}
	go func() { _ = f.backend.Serve(ln) }()
	t.Cleanup(func() { _ = f.backend.Close() })

	// The ACME client must trust pebble's own TLS listener certificate
	// (a self-signed cert generated above); the HTTPS client verifying
	// the issued certificate separately trusts pebble's internal root
	// CA from the management endpoint.
	f.cfg.CARoot = filepath.Join(root, "listener-cert.pem")
	f.mgr = NewManager(f.cfg, stateDir)
	return f
}

// start launches the proxy listeners and the certificate lifecycle. A
// proxy crash fails the test at the next synchronization point.
var serveErr chan error

func (f *integrationFixture) start(ctx context.Context) {
	f.startServe(ctx)
	f.startRun(ctx)
}

func (f *integrationFixture) startServe(ctx context.Context) {
	serveErr = make(chan error, 1)
	go func() { serveErr <- Serve(ctx, f.cfg, f.mgr) }()
	f.t.Cleanup(func() {
		select {
		case err := <-serveErr:
			if err != nil && err != context.Canceled {
				f.t.Errorf("proxy server exited with %v", err)
			}
		default:
		}
	})
}

func (f *integrationFixture) startRun(ctx context.Context) {
	go f.mgr.Run(ctx)
}

func waitForPhase(t *testing.T, f *integrationFixture, phase string, timeout time.Duration) Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := f.mgr.StatusSnapshot()
		if st.Phase == phase {
			return st
		}
		if st.Phase == PhaseError && phase != PhaseError {
			f.t.Fatalf("httpsd entered error phase: %s", st.LastError)
		}
		time.Sleep(200 * time.Millisecond)
	}
	f.t.Fatalf("timed out waiting for phase %s (last: %+v)", phase, f.mgr.StatusSnapshot())
	return Status{}
}

func httpsClientFor(t *testing.T, pool *x509.CertPool) *http.Client {
	t.Helper()
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // observe redirects, don't follow
		},
	}
}

func TestPebbleIssueServeProxyAndRenew(t *testing.T) {
	f := newIntegrationFixture(t)
	pool := pebbleRootCAPool(t)
	client := httpsClientFor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.start(ctx)

	st := waitForPhase(t, f, PhaseActive, 45*time.Second)
	if !st.NotAfter.After(time.Now()) {
		t.Fatalf("issued certificate is not in the future: %+v", st)
	}

	// 1. HTTPS request reaches the loopback backend through the proxy.
	resp, err := client.Get("https://127.0.0.1:" + itoa(testTLSPort) + "/healthz")
	if err != nil {
		t.Fatalf("https request through proxy: %v", err)
	}
	body := make([]byte, 256)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if string(body[:n]) != "backend-ok" {
		t.Fatalf("unexpected backend body %q", string(body[:n]))
	}
	if got := f.backendSeen.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("backend saw X-Forwarded-Proto %q, want https", got)
	}
	if got := f.backendSeen.Get("X-Real-IP"); got == "" {
		t.Fatal("backend did not receive X-Real-IP")
	}

	// 2. Trusted-proxy boundary: inbound forwarding headers are stripped.
	req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1:"+itoa(testTLSPort)+"/echo", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Forwarded-Proto", "http")
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("spoofed-header request: %v", err)
	}
	resp2.Body.Close()
	if got := f.backendSeen.Get("X-Forwarded-For"); got == "1.2.3.4" {
		t.Fatal("client-supplied X-Forwarded-For survived the proxy")
	}
	if got := f.backendSeen.Values("X-Forwarded-For"); len(got) != 1 {
		t.Fatalf("X-Forwarded-For values = %v, want exactly one proxy-set value", got)
	}
	if got := f.backendSeen.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("X-Forwarded-Proto = %q, want https", got)
	}
	if resp2.Header.Get("Strict-Transport-Security") == "" {
		t.Fatal("HTTPS response missing HSTS header")
	}

	// 3. Host passthrough: the backend must see the address the browser used.
	if got := f.backendSeen.Get("X-Forwarded-Host"); got != "127.0.0.1:"+itoa(testTLSPort) {
		t.Fatalf("X-Forwarded-Host = %q", got)
	}

	// 4. Port 80 never exposes the backend: challenge 404s, rest redirects.
	resp3, err := client.Get("http://127.0.0.1:" + itoa(testHTTPPort) + "/onboarding")
	if err != nil {
		t.Fatalf("http request: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("http status = %d, want 308", resp3.StatusCode)
	}
	if loc := resp3.Header.Get("Location"); !strings.HasPrefix(loc, "https://127.0.0.1:"+itoa(testTLSPort)+"/onboarding") {
		t.Fatalf("redirect Location = %q", loc)
	}
	resp4, err := client.Get("http://127.0.0.1:" + itoa(testHTTPPort) + "/.well-known/acme-challenge/unknown-token")
	if err != nil {
		t.Fatalf("unknown challenge request: %v", err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown challenge status = %d, want 404", resp4.StatusCode)
	}

	// 5. Renewal: a forced second issuance increments the counter and
	// swaps in a fresh certificate without dropping the phase.
	before := f.mgr.StatusSnapshot().Renewals
	if err := f.mgr.IssueOnce(context.Background()); err != nil {
		t.Fatalf("renewal: %v", err)
	}
	after := f.mgr.StatusSnapshot()
	if after.Phase != PhaseActive {
		t.Fatalf("phase after renewal = %s", after.Phase)
	}
	if after.Renewals != before+1 {
		t.Fatalf("renewals = %d, want %d", after.Renewals, before+1)
	}

	// 6. Status file exists and carries no secrets.
	stRaw, err := os.ReadFile(StatusPath(f.stateDir))
	if err != nil {
		t.Fatalf("status file: %v", err)
	}
	var stFile Status
	if err := json.Unmarshal(stRaw, &stFile); err != nil {
		t.Fatalf("status file parse: %v", err)
	}
	if stFile.Phase != PhaseActive {
		t.Fatalf("status file phase = %s", stFile.Phase)
	}
	if strings.Contains(string(stRaw), "PRIVATE KEY") || strings.Contains(string(stRaw), "keyAuth") {
		t.Fatal("status file contains secret-looking content")
	}
}

func TestPebbleFailClosedWithoutCert(t *testing.T) {
	f := newIntegrationFixture(t)
	pool := pebbleRootCAPool(t)
	client := httpsClientFor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Listeners up, but no certificate lifecycle yet: a normal handshake
	// must fail closed (no certificate, no TLS service).
	f.startServe(ctx)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		// Wait until the listener accepts TCP so the failure below is a
		// TLS failure, not a connect failure.
		conn, derr := net.DialTimeout("tcp", "127.0.0.1:"+itoa(testTLSPort), time.Second)
		if derr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	resp, err := client.Get("https://127.0.0.1:" + itoa(testTLSPort) + "/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("handshake succeeded without any certificate — not failing closed")
	}

	// Once the certificate lifecycle runs and issues, HTTPS works.
	f.startRun(ctx)
	st := waitForPhase(t, f, PhaseActive, 45*time.Second)
	if st.NotAfter.IsZero() {
		t.Fatal("active status without certificate validity")
	}
	resp, err = client.Get("https://127.0.0.1:" + itoa(testTLSPort) + "/healthz")
	if err != nil {
		t.Fatalf("https after issuance: %v", err)
	}
	resp.Body.Close()
	select {
	case err := <-serveErr:
		t.Fatalf("proxy server exited: %v", err)
	default:
	}
}

func TestShouldRenew(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		st   Status
		want bool
	}{
		{"no cert", Status{}, true},
		{"fresh 160h cert", Status{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(159 * time.Hour)}, false},
		{"2/3 lifetime elapsed", Status{NotBefore: now.Add(-110 * time.Hour), NotAfter: now.Add(50 * time.Hour)}, true},
		{"under 48h remaining", Status{NotBefore: now.Add(-140 * time.Hour), NotAfter: now.Add(20 * time.Hour)}, true},
		{"expired", Status{NotBefore: now.Add(-200 * time.Hour), NotAfter: now.Add(-time.Hour)}, true},
	}
	for _, tc := range cases {
		if got := ShouldRenew(tc.st, now); got != tc.want {
			t.Errorf("%s: ShouldRenew = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
