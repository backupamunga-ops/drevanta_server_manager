// The optional custom-domain certificate slot: overlay watching, SNI
// selection support, issuance and renewal — fully independent from the
// primary (IP) slot. Every function here leaves the primary certificate
// and the top-level Status fields untouched; domain state lives only in
// Status.Domain and the slot memory fields.
package httpsd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// domainOverlayPollInterval is how often the proxy worker re-reads the
// domain overlay file. The apply API waits for the result, so a small
// interval keeps the onboarding/settings UI responsive at negligible
// cost (one stat+read of a small JSON file per tick).
const domainOverlayPollInterval = 2 * time.Second

// equalHostname compares two hostnames case-insensitively, ignoring one
// trailing dot.
func equalHostname(a, b string) bool {
	return NormalizeDomain(a) == NormalizeDomain(b)
}

func (m *Manager) domainCertDir() string {
	return filepath.Join(m.stateDir, "certs-domain")
}

// domainCertIfValid loads the stored domain certificate files and
// returns them only when they parse, cover host, and are currently
// valid (with the same margins as LoadCertIfValid).
func (m *Manager) domainCertIfValid(host string, now time.Time) *tls.Certificate {
	raw, err := os.ReadFile(filepath.Join(m.domainCertDir(), "cert.pem"))
	if err != nil {
		return nil
	}
	keyRaw, err := os.ReadFile(filepath.Join(m.domainCertDir(), "key.pem"))
	if err != nil {
		return nil
	}
	cert, err := tls.X509KeyPair(raw, keyRaw)
	if err != nil {
		return nil
	}
	leaf := cert.Leaf
	if leaf == nil || !certCoversDomain(leaf, host) {
		return nil
	}
	if now.After(leaf.NotAfter.Add(-30*time.Second)) || now.Before(leaf.NotBefore.Add(-time.Minute)) {
		return nil
	}
	return &cert
}

// certCoversDomain reports whether the leaf certificate's DNS SANs
// cover the given hostname (case-insensitive, trailing dot ignored).
func certCoversDomain(leaf *x509.Certificate, domain string) bool {
	for _, n := range leaf.DNSNames {
		if equalHostname(n, domain) {
			return true
		}
	}
	return false
}

// loadDomainCertIfValid is the restart fast path: when the overlay
// requests a domain whose stored certificate is still valid, serve it
// immediately without any ACME traffic.
func (m *Manager) loadDomainCertIfValid(now time.Time) {
	if m.overlayDir == "" {
		return
	}
	ov, err := LoadDomainOverlay(m.overlayDir)
	if err != nil || ov.Domain == "" {
		return
	}
	if cert := m.domainCertIfValid(ov.Domain, now); cert != nil {
		m.setDomainServed(ov.Domain, cert)
	}
}

// watchDomainOverlay applies overlay changes (set / clear / change the
// domain) until ctx is cancelled. Domain issuance is serialized through
// domainBusy; concurrent ACME account loads are serialized by acmeMu.
func (m *Manager) watchDomainOverlay(ctx context.Context) {
	ticker := time.NewTicker(domainOverlayPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if m.overlayDir == "" {
			continue
		}
		ov, err := LoadDomainOverlay(m.overlayDir)
		if err != nil {
			slog.Warn("httpsd: domain overlay unreadable", "err", err)
			continue
		}
		desired := ov.Domain // already normalized; "" = none
		m.mu.Lock()
		current := m.domain
		m.mu.Unlock()
		if desired == current {
			continue
		}
		if err := m.transitionDomain(ctx, desired); err != nil {
			slog.Error("httpsd: domain slot transition failed", "domain", desired, "err", err)
		}
	}
}

// transitionDomain moves the domain slot to the desired state. The IP
// primary slot is never modified here — a domain that cannot issue only
// ever fails its own slot.
func (m *Manager) transitionDomain(ctx context.Context, desired string) error {
	if desired == "" {
		m.clearDomainSlot()
		slog.Info("httpsd: custom domain removed; the primary IP certificate keeps serving")
		return nil
	}
	// Record the desired domain first so error/issuing statuses attach to
	// it and SNI routing can immediately reach a still-valid stored
	// certificate (restart fast path).
	m.mu.Lock()
	m.domain = desired
	m.status.Domain = &DomainStatus{Host: desired, Phase: PhaseIssuing, UpdatedAt: time.Now().UTC()}
	m.mu.Unlock()
	_ = WriteStatus(m.stateDir, m.StatusSnapshot())

	if isIPIdentifier(desired) {
		m.setDomainError(desired, errors.New("the domain overlay must carry a DNS name; IP addresses belong to the primary slot"))
		return nil
	}
	if err := ValidHostname(desired); err != nil {
		m.setDomainError(desired, err)
		return nil
	}
	if !m.domainBusy.CompareAndSwap(false, true) {
		return nil // another transition is running; the watcher retries
	}
	defer m.domainBusy.Store(false)

	if cert := m.domainCertIfValid(desired, time.Now()); cert != nil {
		m.setDomainServed(desired, cert)
		slog.Info("httpsd: serving stored domain certificate", "host", desired)
		return nil
	}

	// Short retry ladder (same shape as the primary issuance) so a slow
	// DNS propagation or transient CA hiccup does not force an hourly
	// wait for the onboarding UI.
	var last error
	for _, d := range []time.Duration{0, 15 * time.Second, 60 * time.Second} {
		if d > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
		}
		client, err := m.loadAccount(ctx)
		if err == nil {
			err = m.issueFor(ctx, client, desired, "certs-domain")
		}
		if err == nil {
			return nil
		}
		last = err
		m.setDomainError(desired, err)
		slog.Warn("httpsd: domain issuance attempt failed", "domain", desired, "err", err)
	}
	return last
}

// renewDomain re-issues the domain certificate (hourly check). Failure
// keeps the previous certificate serving while it remains valid.
func (m *Manager) renewDomain(ctx context.Context, host string) {
	if !m.domainBusy.CompareAndSwap(false, true) {
		return
	}
	defer m.domainBusy.Store(false)
	m.setDomainPhase(host, PhaseRenewing)
	client, err := m.loadAccount(ctx)
	if err == nil {
		err = m.issueFor(ctx, client, host, "certs-domain")
	}
	if err == nil {
		return
	}
	m.setDomainError(host, err)
	slog.Error("httpsd: domain renewal failed (will retry hourly)", "host", host, "err", err)
	// Keep serving the stored certificate while it is still valid.
	if cert := m.domainCertIfValid(host, time.Now()); cert != nil {
		m.setDomainServed(host, cert)
	}
}

// setDomainServed swaps the domain slot to an active certificate. A
// completion for a domain that is no longer desired (overlay changed
// mid-issuance) is discarded.
func (m *Manager) setDomainServed(host string, cert *tls.Certificate) {
	m.mu.Lock()
	if m.domain != host || cert == nil || cert.Leaf == nil {
		m.mu.Unlock()
		return
	}
	leaf := cert.Leaf
	m.domainServed = cert
	m.status.Domain = &DomainStatus{
		Host:      host,
		Phase:     PhaseActive,
		NotBefore: leaf.NotBefore,
		NotAfter:  leaf.NotAfter,
		UpdatedAt: time.Now().UTC(),
	}
	cur := m.status
	m.mu.Unlock()
	_ = WriteStatus(m.stateDir, cur)
}

// setDomainPhase records a phase change for the domain slot (issuing,
// renewing) without touching the primary status fields.
func (m *Manager) setDomainPhase(host, phase string) {
	m.mu.Lock()
	if m.domain != host {
		m.mu.Unlock()
		return
	}
	ds := &DomainStatus{Host: host, Phase: phase, UpdatedAt: time.Now().UTC()}
	if prev := m.status.Domain; prev != nil && prev.Host == host {
		ds.NotBefore = prev.NotBefore
		ds.NotAfter = prev.NotAfter
	}
	m.status.Domain = ds
	cur := m.status
	m.mu.Unlock()
	_ = WriteStatus(m.stateDir, cur)
}

// setDomainError records a domain-slot failure. It never modifies the
// primary slot or its certificate.
func (m *Manager) setDomainError(host string, err error) {
	m.mu.Lock()
	if m.domain != host {
		m.mu.Unlock()
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.status.Domain = &DomainStatus{
		Host:      host,
		Phase:     PhaseError,
		LastError: msg,
		UpdatedAt: time.Now().UTC(),
	}
	cur := m.status
	m.mu.Unlock()
	slog.Warn("httpsd: domain slot error recorded", "host", host, "err", msg)
	_ = WriteStatus(m.stateDir, cur)
}

// clearDomainSlot stops serving the domain certificate and clears the
// overlay state. Stored files are kept for a fast re-add.
func (m *Manager) clearDomainSlot() {
	m.mu.Lock()
	m.domain = ""
	m.domainServed = nil
	m.status.Domain = nil
	cur := m.status
	m.mu.Unlock()
	_ = WriteStatus(m.stateDir, cur)
}
