// Unit tests for the optional custom-domain overlay and the SNI-based
// certificate selection of the dual-slot manager. The primary (IP)
// slot must be completely unaffected by every domain operation.
package httpsd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"testing"
	"time"
)

// genSlotCert builds a self-signed certificate for tests: either DNS
// SANs or IP SANs, with a parsed Leaf (the manager requires Leaf).
func genSlotCert(t *testing.T, dns []string, ips []net.IP) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "slot-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              dns,
		IPAddresses:           ips,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("key der: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	return &pair
}

func TestValidHostname(t *testing.T) {
	valid := []string{
		"vpn.example.com", "VPN.Example.COM.", "a-b.c.example.co.uk",
		"xn--bcher-kva.example.com", "123.example.com", "d.dev",
	}
	for _, in := range valid {
		if err := ValidHostname(in); err != nil {
			t.Errorf("ValidHostname(%q) = %v, want nil", in, err)
		}
	}
	invalid := map[string]string{
		"":                      "empty",
		"localhost":             "single label",
		"203.0.113.7":           "IP literal",
		"*.example.com":         "wildcard",
		"my_server.example.com": "underscore",
		"-led.example.com":      "leading hyphen",
		"led-.example.com":      "trailing hyphen",
		"example":               "no dot",
		"exa mple.com":          "space",
		"exa/path.com":          "slash",
		"mail.example.com:443":  "port",
		"user@mail.example.com": "at sign",
	}
	for in, why := range invalid {
		if err := ValidHostname(in); err == nil {
			t.Errorf("ValidHostname(%q) accepted (%s)", in, why)
		}
	}
}

func TestOverlaySaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()

	// Missing file: empty overlay, no error.
	ov, err := LoadDomainOverlay(dir)
	if err != nil || ov.Domain != "" {
		t.Fatalf("missing overlay = (%q, %v)", ov.Domain, err)
	}

	if err := SaveDomainOverlay(dir, "VPN.Example.COM."); err != nil {
		t.Fatalf("save: %v", err)
	}
	ov, err = LoadDomainOverlay(dir)
	if err != nil || ov.Domain != "vpn.example.com" {
		t.Fatalf("roundtrip = (%q, %v), want vpn.example.com", ov.Domain, err)
	}
	info, err := os.Stat(OverlayPath(dir))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("overlay perms = %v, want 0644 (secret-free, proxy-readable)", info)
	}

	// Empty domain removes the file.
	if err := SaveDomainOverlay(dir, ""); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	if _, err := os.Stat(OverlayPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("overlay still present after clearing: %v", err)
	}
}

func TestOverlayCorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(OverlayPath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDomainOverlay(dir); err == nil {
		t.Fatal("corrupt overlay must be an error so the watcher never guesses")
	}
}

func TestServedCertForSNI(t *testing.T) {
	ipCert := genSlotCert(t, nil, []net.IP{net.ParseIP("203.0.113.7")})
	domCert := genSlotCert(t, []string{"vpn.example.com"}, nil)

	cfg := Default()
	cfg.Host = "203.0.113.7"
	m := NewManager(cfg, t.TempDir(), t.TempDir())
	m.mu.Lock()
	m.served = ipCert
	m.domain = "vpn.example.com"
	m.domainServed = domCert
	m.mu.Unlock()

	cases := []struct {
		sni  string
		want *tls.Certificate
	}{
		{"vpn.example.com", domCert},
		{"VPN.Example.COM.", domCert}, // case + trailing dot are the same name
		{"", ipCert},                  // RFC 8738 IP validation: no SNI
		{"other.example.com", ipCert}, // unknown SNI falls back to primary
		{"203.0.113.7", ipCert},
	}
	for _, tc := range cases {
		hello := &tls.ClientHelloInfo{ServerName: tc.sni}
		got, err := m.ChallengeHook()(hello)
		if err != nil {
			t.Fatalf("SNI %q: hook error %v", tc.sni, err)
		}
		if got != tc.want {
			t.Errorf("SNI %q: wrong certificate selected", tc.sni)
		}
	}
}

func TestSetDomainServedStaleCompletionDiscarded(t *testing.T) {
	cfg := Default()
	cfg.Host = "203.0.113.7"
	m := NewManager(cfg, t.TempDir(), t.TempDir())
	m.mu.Lock()
	m.domain = "new.example.com"
	m.mu.Unlock()

	stale := genSlotCert(t, []string{"old.example.com"}, nil)
	m.setDomainServed("old.example.com", stale) // overlay moved on mid-issuance
	if m.StatusSnapshot().Domain != nil {
		t.Fatal("a completion for a superseded domain must be discarded")
	}

	cur := genSlotCert(t, []string{"new.example.com"}, nil)
	m.setDomainServed("new.example.com", cur)
	ds := m.StatusSnapshot().Domain
	if ds == nil || ds.Phase != PhaseActive || ds.Host != "new.example.com" {
		t.Fatalf("domain status = %+v, want active for new.example.com", ds)
	}
	// Primary untouched.
	if m.StatusSnapshot().Phase == PhaseActive {
		t.Fatal("domain activation must not mark the primary slot active")
	}
}

func TestTransitionDomainRejectsIPAndInvalid(t *testing.T) {
	cfg := Default()
	cfg.Host = "203.0.113.7"
	dir := t.TempDir()
	m := NewManager(cfg, dir, dir)

	m.transitionDomain(t.Context(), "198.51.100.9")
	if ds := m.StatusSnapshot().Domain; ds == nil || ds.Phase != PhaseError {
		t.Fatalf("IP overlay = %+v, want domain-slot error (IPs belong to the primary slot)", ds)
	}

	m.transitionDomain(t.Context(), "not a domain")
	if ds := m.StatusSnapshot().Domain; ds == nil || ds.Phase != PhaseError {
		t.Fatalf("invalid overlay = %+v, want error", ds)
	}

	// The primary slot state is untouched by both.
	snap := m.StatusSnapshot()
	if snap.Host != "" && snap.Phase == PhaseActive {
		t.Fatal("primary slot must stay inactive when no primary cert exists")
	}
}

func TestClearDomainSlot(t *testing.T) {
	cfg := Default()
	cfg.Host = "203.0.113.7"
	m := NewManager(cfg, t.TempDir(), t.TempDir())
	domCert := genSlotCert(t, []string{"vpn.example.com"}, nil)
	m.mu.Lock()
	m.domain = "vpn.example.com"
	m.domainServed = domCert
	m.status.Domain = &DomainStatus{Host: "vpn.example.com", Phase: PhaseActive}
	m.mu.Unlock()

	m.clearDomainSlot()
	if m.StatusSnapshot().Domain != nil {
		t.Fatal("clearDomainSlot must clear the domain status")
	}
	if m.servedCertForSNI("vpn.example.com") != nil {
		t.Fatal("no domain certificate may be served after clearing")
	}
}
