package httpsd

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/tlsalpn01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// Renewal policy: check hourly; renew when two thirds of the validity
// have elapsed (for the 160-hour shortlived profile that is ~2.2 days
// before expiry) or when fewer than 48 hours remain.
const (
	renewCheckInterval = time.Hour
	renewAtFraction    = 2
	renewAtDivisor     = 3
	renewMinRemaining  = 48 * time.Hour
	acmeOrderTimeout   = 90 * time.Second
)

// ShouldRenew is the pure renewal decision (unit-tested).
func ShouldRenew(st Status, now time.Time) bool {
	if st.NotAfter.IsZero() || !now.Before(st.NotAfter) {
		return true
	}
	life := st.NotAfter.Sub(st.NotBefore)
	if life <= 0 {
		return true
	}
	if now.Sub(st.NotBefore) >= life*renewAtFraction/renewAtDivisor {
		return true
	}
	return st.NotAfter.Sub(now) < renewMinRemaining
}

// ---------------------------------------------------------------------------
// ACME account (lego registration.User)
// ---------------------------------------------------------------------------

type acmeUser struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// loadOrCreateAccountKey returns the persistent ECDSA P-256 account key.
func loadOrCreateAccountKey(stateDir string) (crypto.PrivateKey, error) {
	path := filepath.Join(stateDir, "acme-account.key")
	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, errors.New("corrupt ACME account key")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	// 0600: the account key is the identity of this ACME client.
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// ---------------------------------------------------------------------------
// Challenge store: one proxy process owns ports 80/443, so the ACME
// challenge responses are served by the same listeners as real traffic.
// ---------------------------------------------------------------------------

type challengeStore struct {
	mu      sync.Mutex
	http01  map[string]string           // token -> keyAuth
	tlsalpn map[string]*tls.Certificate // host -> challenge certificate
}

func newChallengeStore() *challengeStore {
	return &challengeStore{http01: map[string]string{}, tlsalpn: map[string]*tls.Certificate{}}
}

// Present answers an http-01 challenge (served by the :80 handler).
func (c *challengeStore) Present(domain, token, keyAuth string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.http01[token] = keyAuth
	return nil
}

func (c *challengeStore) CleanUp(domain, token, keyAuth string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.http01, token)
	return nil
}

// presentTLSALPN answers a tls-alpn-01 challenge: build the special
// certificate and remember it until the handshake that consumes it.
func (c *challengeStore) presentTLSALPN(domain, token, keyAuth string) error {
	cert, err := tlsalpn01.ChallengeCert(domain, keyAuth)
	if err != nil {
		return fmt.Errorf("build tls-alpn-01 challenge cert: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tlsalpn[domain] = cert
	return nil
}

func (c *challengeStore) cleanUpTLSALPN(domain, token, keyAuth string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tlsalpn, domain)
	return nil
}

// http01KeyAuth returns the key authorization for a challenge token.
func (c *challengeStore) http01KeyAuth(token string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ka, ok := c.http01[token]
	return ka, ok
}

// challengeCertFor implements the tls-alpn-01 handshake hook: when the
// client offers the ACME ALPN protocol and a challenge is pending, the
// challenge certificate is the only correct answer.
func (c *challengeStore) challengeCertFor(hello *tls.ClientHelloInfo) (*tls.Certificate, bool) {
	const acmeTLS1 = "acme-tls/1"
	off := false
	for _, p := range hello.SupportedProtos {
		if p == acmeTLS1 {
			off = true
			break
		}
	}
	if !off {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Preferred: exact match on the SNI the validator sent (DNS names;
	// pebble sends the reverse-DNS form for IP identifiers).
	if cert, ok := c.tlsalpn[hello.ServerName]; ok {
		return cert, true
	}
	// IP-identifier orders carry no standardised SNI (RFC 6066 has no IP
	// form) and different validators pick different conventions. This
	// proxy serves exactly one host, so at most one challenge can be
	// pending — and a challenge certificate is only ever present for the
	// few seconds of an active validation, bound to that validation's
	// key authorization. Serving it to any acme-tls/1 handshake is safe.
	if len(c.tlsalpn) == 1 {
		for _, cert := range c.tlsalpn {
			return cert, true
		}
	}
	return nil, false
}

// tlsALPNProvider adapts the store to lego's challenge.Provider.
type tlsALPNProvider struct{ store *challengeStore }

func (p tlsALPNProvider) Present(domain, token, keyAuth string) error {
	return p.store.presentTLSALPN(domain, token, keyAuth)
}
func (p tlsALPNProvider) CleanUp(domain, token, keyAuth string) error {
	return p.store.cleanUpTLSALPN(domain, token, keyAuth)
}

// Compile-time interface checks.
var (
	_ challenge.Provider = (*challengeStore)(nil)
	_ challenge.Provider = tlsALPNProvider{}
)

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager owns certificate lifecycle for one configured host.
type Manager struct {
	cfg      Config
	stateDir string
	store    *challengeStore

	mu     sync.Mutex
	served *tls.Certificate // currently served certificate (nil = none)
	status Status
}

// NewManager builds the manager; no I/O happens here.
func NewManager(cfg Config, stateDir string) *Manager {
	return &Manager{cfg: cfg, stateDir: stateDir, store: newChallengeStore()}
}

// ServedCert returns the certificate to use for normal handshakes, or
// nil when none is valid (fail closed: no cert, no TLS service).
func (m *Manager) ServedCert() *tls.Certificate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.served
}

// StatusSnapshot returns the current status for the status file.
func (m *Manager) StatusSnapshot() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) setStatus(phase string) {
	m.mu.Lock()
	m.status.Phase = phase
	cur := m.status
	m.mu.Unlock()
	if err := WriteStatus(m.stateDir, cur); err != nil {
		slog.Error("httpsd: write status", "err", err)
	}
}

func (m *Manager) setStatusError(err error) {
	m.mu.Lock()
	m.status.LastError = err.Error()
	m.status.LastErrorAt = time.Now().UTC()
	cur := m.status
	m.mu.Unlock()
	if werr := WriteStatus(m.stateDir, cur); werr != nil {
		slog.Error("httpsd: write status", "err", werr)
	}
}

func (m *Manager) setStatusFull(st Status) {
	m.mu.Lock()
	m.status = st
	m.mu.Unlock()
	if err := WriteStatus(m.stateDir, st); err != nil {
		slog.Error("httpsd: write status", "err", err)
	}
}

// ChallengeHook returns the GetCertificate hook combining the ACME
// tls-alpn-01 answer with the currently served certificate.
func (m *Manager) ChallengeHook() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if cert, ok := m.store.challengeCertFor(hello); ok {
			return cert, nil
		}
		if cert := m.ServedCert(); cert != nil {
			return cert, nil
		}
		return nil, errors.New("httpsd: no certificate available for this handshake")
	}
}

// HTTP01ChallengeValue exposes the key authorization for the token (the
// :80 handler controls status and headers itself).
func (m *Manager) HTTP01ChallengeValue(token string) (string, bool) {
	return m.store.http01KeyAuth(token)
}

// loadAccount builds the lego client with the persistent account.
func (m *Manager) loadAccount(ctx context.Context) (*lego.Client, error) {
	key, err := loadOrCreateAccountKey(m.stateDir)
	if err != nil {
		return nil, fmt.Errorf("acme account key: %w", err)
	}
	user := &acmeUser{email: m.cfg.Email, key: key}

	cfg := lego.NewConfig(user)
	cfg.CADirURL = m.cfg.ACMEDir
	cfg.Certificate.Timeout = acmeOrderTimeout
	cfg.Certificate.DisableCommonName = true
	if m.cfg.CARoot != "" {
		pool, err := lego.CreateCertPool([]string{m.cfg.CARoot}, true)
		if err != nil {
			return nil, fmt.Errorf("acme_ca_root %s: %w", m.cfg.CARoot, err)
		}
		cfg.HTTPClient = clientWithRoot{root: pool}.client()
	}

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	// Challenge solvers are backed by the proxy's own :80/:443 listeners.
	if err := client.Challenge.SetHTTP01Provider(m.store); err != nil {
		return nil, err
	}
	if err := client.Challenge.SetTLSALPN01Provider(tlsALPNProvider{store: m.store}); err != nil {
		return nil, err
	}

	// Register or reuse the ACME account.
	regFile := filepath.Join(m.stateDir, "acme-registration.json")
	if raw, err := os.ReadFile(regFile); err == nil {
		var reg registration.Resource
		if jerr := json.Unmarshal(raw, &reg); jerr == nil {
			user.reg = &reg
			if _, rerr := client.Registration.ResolveAccountByKey(); rerr == nil {
				return client, nil
			}
			// Key no longer known to the server: re-register below.
			user.reg = nil
		}
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, fmt.Errorf("acme registration: %w", err)
	}
	user.reg = reg
	if raw, jerr := json.Marshal(reg); jerr == nil {
		_ = os.WriteFile(regFile, raw, 0o600)
	}
	return client, nil
}

// resolveProfile applies the requested ACME profile only when the CA
// actually advertises it in its directory metadata (ACME profiles
// extension). Let's Encrypt advertises "shortlived"; smaller test CAs
// (pebble) do not — omitting the field there keeps the same code path
// testable, and for IP identifiers Let's Encrypt selects a short-lived
// certificate automatically even without the profile field.
func (m *Manager) resolveProfile() string {
	httpClient := http.DefaultClient
	if m.cfg.CARoot != "" {
		pool, err := lego.CreateCertPool([]string{m.cfg.CARoot}, true)
		if err == nil {
			hc := clientWithRoot{root: pool}.client()
			hc.Timeout = 15 * time.Second
			httpClient = hc
		}
	}
	req, err := http.NewRequest(http.MethodGet, m.cfg.ACMEDir, nil)
	if err != nil {
		return ""
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Warn("httpsd: could not fetch ACME directory metadata", "err", err)
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var meta struct {
		Profiles map[string]string `json:"profiles"`
	}
	if json.Unmarshal(body, &meta) != nil {
		return ""
	}
	if _, ok := meta.Profiles[m.cfg.Profile]; ok {
		return m.cfg.Profile
	}
	slog.Warn("httpsd: CA does not advertise the requested profile; requesting a default-profile order",
		"requested", m.cfg.Profile, "directory", m.cfg.ACMEDir)
	return ""
}

// LoadCertIfValid loads a previously issued certificate from disk and,
// when still valid, starts serving it immediately (fast restarts, no
// pointless ACME traffic). Returns true when a valid cert is active.
func (m *Manager) LoadCertIfValid() (bool, error) {
	certPath := filepath.Join(m.stateDir, "certs", "cert.pem")
	keyPath := filepath.Join(m.stateDir, "certs", "key.pem")
	raw, err := os.ReadFile(certPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	keyRaw, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	cert, err := tls.X509KeyPair(raw, keyRaw)
	if err != nil {
		return false, fmt.Errorf("parse stored certificate: %w", err)
	}
	leaf := cert.Leaf
	if leaf == nil {
		return false, errors.New("stored certificate has no parsed leaf")
	}
	now := time.Now()
	if now.After(leaf.NotAfter.Add(-30*time.Second)) || now.Before(leaf.NotBefore.Add(-time.Minute)) {
		return false, nil
	}
	m.mu.Lock()
	m.served = &cert
	m.status.Phase = PhaseActive
	m.status.Host = m.cfg.Host
	m.status.Profile = m.cfg.Profile
	m.status.Staging = m.cfg.Staging
	m.status.NotBefore = leaf.NotBefore
	m.status.NotAfter = leaf.NotAfter
	cur := m.status
	m.mu.Unlock()
	_ = WriteStatus(m.stateDir, cur)
	return true, nil
}

// IssueOnce obtains (or re-obtains) the certificate for cfg.Host via a
// fresh ACME order and swaps it into the live TLS configuration.
func (m *Manager) IssueOnce(ctx context.Context) error {
	client, err := m.loadAccount(ctx)
	if err != nil {
		return err
	}
	req := certificate.ObtainRequest{
		Domains: []string{m.cfg.Host},
		Profile: m.resolveProfile(),
		Bundle:  true,
	}
	res, err := client.Certificate.Obtain(req)
	if err != nil {
		return fmt.Errorf("acme order for %s: %w", m.cfg.Host, err)
	}

	certDir := filepath.Join(m.stateDir, "certs")
	if err := os.MkdirAll(certDir, 0o750); err != nil {
		return err
	}
	// 0600: the certificate private key never leaves this directory.
	if err := os.WriteFile(filepath.Join(certDir, "cert.pem"), res.Certificate, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "issuer.pem"), res.IssuerCertificate, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "key.pem"), res.PrivateKey, 0o600); err != nil {
		return err
	}

	cert, err := tls.X509KeyPair(res.Certificate, res.PrivateKey)
	if err != nil {
		return fmt.Errorf("parse issued certificate: %w", err)
	}
	leaf := cert.Leaf
	if leaf == nil {
		return errors.New("issued certificate has no parsed leaf")
	}

	m.mu.Lock()
	prev := m.status
	renewals := prev.Renewals
	if prev.Phase == PhaseActive || prev.Phase == PhaseRenewing {
		renewals++
	}
	m.served = &cert
	m.status = Status{
		Phase:     PhaseActive,
		Host:      m.cfg.Host,
		Profile:   m.cfg.Profile,
		Staging:   m.cfg.Staging,
		NotBefore: leaf.NotBefore,
		NotAfter:  leaf.NotAfter,
		IssuedAt:  time.Now().UTC(),
		Renewals:  renewals,
	}
	cur := m.status
	m.mu.Unlock()
	slog.Info("httpsd: certificate active", "host", m.cfg.Host,
		"not_after", leaf.NotAfter.Format(time.RFC3339), "renewals", renewals)
	return WriteStatus(m.stateDir, cur)
}

// Run is the httpsd certificate lifecycle: initial issue (with a short
// retry ladder), then an hourly renewal loop for the life of the
// process. Issuance failures are recorded and retried; they never stop
// an already-valid certificate from being served.
func (m *Manager) Run(ctx context.Context) {
	st := m.StatusSnapshot()
	st.Host = m.cfg.Host
	st.Profile = m.cfg.Profile
	st.Staging = m.cfg.Staging
	m.setStatusFull(st)

	if valid, err := m.LoadCertIfValid(); err != nil {
		m.setStatusError(err)
	} else if valid && !ShouldRenew(m.StatusSnapshot(), time.Now()) {
		slog.Info("httpsd: serving stored certificate", "host", m.cfg.Host)
	} else {
		if valid {
			m.setStatus(PhaseRenewing)
		} else {
			m.setStatus(PhaseIssuing)
		}
		if err := m.issueWithRetry(ctx); err != nil {
			slog.Error("httpsd: initial issuance failed", "err", err)
			m.setStatusError(err)
		}
	}

	ticker := time.NewTicker(renewCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		snap := m.StatusSnapshot()
		if !ShouldRenew(snap, now) {
			continue
		}
		if snap.Phase == PhaseActive {
			m.setStatus(PhaseRenewing)
		} else {
			m.setStatus(PhaseIssuing)
		}
		if err := m.IssueOnce(ctx); err != nil {
			slog.Error("httpsd: renewal failed (will retry hourly)", "err", err)
			m.setStatusError(err)
			// Keep serving the old certificate while it is still valid;
			// only when it has actually expired does the proxy fail closed.
			if snap.NotAfter.After(now) {
				m.mu.Lock()
				m.status.Phase = PhaseActive
				m.mu.Unlock()
				if werr := WriteStatus(m.stateDir, m.StatusSnapshot()); werr != nil {
					slog.Error("httpsd: write status", "err", werr)
				}
			}
		}
	}
}

// issueWithRetry runs IssueOnce with a short backoff ladder so a slow
// firewall change or transient CA hiccup during installation does not
// force a full hourly wait.
func (m *Manager) issueWithRetry(ctx context.Context) error {
	delays := []time.Duration{0, 15 * time.Second, 60 * time.Second}
	var last error
	for _, d := range delays {
		if d > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
		}
		if err := m.IssueOnce(ctx); err == nil {
			return nil
		} else {
			last = err
			slog.Warn("httpsd: issuance attempt failed", "err", err)
		}
	}
	return last
}
