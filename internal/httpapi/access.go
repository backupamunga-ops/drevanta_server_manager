// Trusted access-mode and custom-domain endpoints.
//
// The dashboard must never guess its own access mode from the browser
// URL: the label comes from here, i.e. from the root-written access
// configuration and the proxy's secret-free status file — the same
// sources the claim screen and the `drevanta access` CLI use.
//
// The custom-domain endpoints implement the onboarding/settings flow:
//
//	check  — hostname shape + DNS A/AAAA must point at this VPS
//	apply  — DNS verified → overlay written → certificate active →
//	         full route verification (TLS + proxy + backend) → only
//	         then success; ANY failure removes the overlay again, so
//	         the working IP HTTPS is never disturbed
//	remove — clear the overlay; the IP certificate keeps serving
//
// The overlay file is secret-free JSON in a daemon-writable directory;
// the httpsd proxy worker picks it up and manages the second slot.
package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/httpsd"
)

// AccessView carries the runtime paths and seams the access endpoints
// need. A nil AccessView (tests of the pure identity surface) makes the
// access endpoints answer 503 rather than guess.
type AccessView struct {
	ConfigPath    string
	HTTPSStateDir string
	OverlayDir    string

	// Test seams; zero values use the production implementations.
	Now         func() time.Time
	LookupIP    func(ctx context.Context, domain string) ([]net.IP, error)
	PublicIPs   func() []string
	VerifyRoute func(ctx context.Context, domain string, httpsPort int) error
	WaitTimeout time.Duration // default 90s
	PollEvery   time.Duration // default 500ms
}

func (av *AccessView) now() time.Time {
	if av.Now != nil {
		return av.Now()
	}
	return time.Now()
}

func (av *AccessView) waitTimeout() time.Duration {
	if av.WaitTimeout > 0 {
		return av.WaitTimeout
	}
	return 90 * time.Second
}

func (av *AccessView) pollEvery() time.Duration {
	if av.PollEvery > 0 {
		return av.PollEvery
	}
	return 500 * time.Millisecond
}

func (av *AccessView) lookupIP(ctx context.Context, domain string) ([]net.IP, error) {
	if av.LookupIP != nil {
		return av.LookupIP(ctx, domain)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

func (av *AccessView) publicIPs(cfg httpsd.Config) []string {
	if av.PublicIPs != nil {
		return av.PublicIPs()
	}
	var out []string
	if ip := net.ParseIP(cfg.Host); ip != nil {
		out = append(out, ip.String())
	}
	if ip, _ := httpsd.DetectPublicIP(); ip != "" {
		out = append(out, ip)
	}
	return out
}

// verifyRoute is the production route check: dial the loopback TLS
// listener with the domain as SNI (DNS-independent by construction),
// request the backend through the proxy, and require a 200. The domain
// certificate must validate against the system trust store — exactly
// what a browser will do.
func (av *AccessView) verifyRoute(ctx context.Context, domain string, httpsPort int) error {
	if av.VerifyRoute != nil {
		return av.VerifyRoute(ctx, domain, httpsPort)
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", httpsPort))
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(dctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(dctx, network, addr)
			},
			TLSClientConfig: &tls.Config{
				ServerName: domain,
				MinVersion: tls.VersionTLS12,
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("TLS + route check for %s failed: %w", domain, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("route check for %s: backend answered HTTP %d, want 200", domain, resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GET /api/v1/access/status (public; secret-free)
// ---------------------------------------------------------------------------

type domainStatusJSON struct {
	Host      string `json:"host"`
	Phase     string `json:"phase"`
	CertValid bool   `json:"cert_valid"`
	NotAfter  string `json:"not_after,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type httpsStatusJSON struct {
	Enabled   bool              `json:"enabled"`
	Host      string            `json:"host,omitempty"`
	Phase     string            `json:"phase,omitempty"`
	CertValid bool              `json:"cert_valid"`
	NotAfter  string            `json:"not_after,omitempty"`
	Staging   bool              `json:"staging,omitempty"`
	Domain    *domainStatusJSON `json:"domain,omitempty"`
}

type accessStatusResponse struct {
	Mode  string           `json:"mode"` // "https" | "private"
	HTTPS *httpsStatusJSON `json:"https,omitempty"`
}

func (s *server) handleAccessStatus(w http.ResponseWriter, r *http.Request) {
	resp := accessStatusResponse{Mode: "private"}
	if s.access == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	cfg, err := httpsd.LoadRuntimeConfig(httpsd.RuntimePaths{ConfigPath: s.access.ConfigPath, StateDir: s.access.HTTPSStateDir})
	if err != nil {
		// A corrupt config must not lie about the access mode.
		slog.Warn("access status: config unreadable", "err", err)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	st, err := httpsd.ReadStatus(s.access.HTTPSStateDir)
	if err != nil {
		slog.Warn("access status: status file unreadable", "err", err)
	}
	now := s.access.now()
	hs := &httpsStatusJSON{
		Enabled:   cfg.Enabled,
		Host:      cfg.Host,
		Phase:     st.Phase,
		CertValid: cfg.Enabled && st.CertValid(now),
		Staging:   cfg.Staging,
	}
	if !st.NotAfter.IsZero() {
		hs.NotAfter = st.NotAfter.UTC().Format(time.RFC3339)
	}
	if ds := st.Domain; ds != nil && ds.Host != "" {
		hs.Domain = &domainStatusJSON{
			Host:      ds.Host,
			Phase:     ds.Phase,
			CertValid: ds.CertValid(now),
			LastError: ds.LastError,
		}
		if !ds.NotAfter.IsZero() {
			hs.Domain.NotAfter = ds.NotAfter.UTC().Format(time.RFC3339)
		}
	}
	if hs.CertValid || (hs.Domain != nil && hs.Domain.CertValid) {
		resp.Mode = "https"
	}
	resp.HTTPS = hs
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Domain request bodies and shared preconditions
// ---------------------------------------------------------------------------

type domainRequest struct {
	Domain string `json:"domain"`
}

// requireDomainReady validates the shared preconditions of the domain
// endpoints and returns the loaded config. It writes the error response
// itself and returns nil when a precondition fails.
func (s *server) requireDomainReady(w http.ResponseWriter, r *http.Request, domain string) *httpsd.Config {
	if s.access == nil || s.access.ConfigPath == "" {
		writeError(w, http.StatusServiceUnavailable, "access_unavailable",
			"the access configuration is not available to this daemon")
		return nil
	}
	if err := httpsd.ValidHostname(domain); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_domain", err.Error())
		return nil
	}
	cfg, err := httpsd.LoadRuntimeConfig(httpsd.RuntimePaths{ConfigPath: s.access.ConfigPath, StateDir: s.access.HTTPSStateDir})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "access configuration unreadable")
		return nil
	}
	if !cfg.Enabled || cfg.Host == "" {
		writeError(w, http.StatusConflict, "https_disabled",
			"public HTTPS is not enabled; enable it first (sudo drevanta access enable-https)")
		return nil
	}
	if net.ParseIP(cfg.Host) == nil {
		writeError(w, http.StatusConflict, "primary_is_domain",
			"the primary HTTPS host is already a custom domain; change it with: sudo drevanta access configure")
		return nil
	}
	return &cfg
}

// dnsMatchesVPS resolves domain and compares the answers with the VPS's
// own public addresses. It returns the resolved and expected values so
// the UI can explain a mismatch.
func (s *server) dnsMatchesVPS(ctx context.Context, cfg *httpsd.Config, domain string) (resolved, expected []string, matches bool, err error) {
	ips, err := s.access.lookupIP(ctx, domain)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return nil, s.access.publicIPs(*cfg), false, fmt.Errorf("DNS lookup for %s failed: %s", domain, dnsErr.Err)
		}
		return nil, s.access.publicIPs(*cfg), false, fmt.Errorf("DNS lookup for %s failed", domain)
	}
	for _, ip := range ips {
		resolved = append(resolved, ip.String())
	}
	expected = s.access.publicIPs(*cfg)
	for _, got := range resolved {
		for _, want := range expected {
			if got == want {
				return resolved, expected, true, nil
			}
		}
	}
	return resolved, expected, false, nil
}

func (s *server) handleDomainCheck(w http.ResponseWriter, r *http.Request) {
	var req domainRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	domain := httpsd.NormalizeDomain(req.Domain)
	cfg := s.requireDomainReady(w, r, domain)
	if cfg == nil {
		return
	}
	resolved, expected, matches, derr := s.dnsMatchesVPS(r.Context(), cfg, domain)
	if derr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"domain": domain, "matches": false, "message": derr.Error(),
		})
		return
	}
	msg := "DNS is set up correctly: " + domain + " points at this server."
	if !matches {
		msg = "DNS for " + domain + " does not point at this server yet. Add an A/AAAA record and retry (propagation can take a while)."
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"domain":   domain,
		"resolved": resolved,
		"expected": expected,
		"matches":  matches,
		"message":  msg,
	})
}

func (s *server) handleDomainApply(w http.ResponseWriter, r *http.Request) {
	// The certificate wait below legitimately holds this response open for
	// up to waitTimeout (default 90s). Extend this request's write
	// deadline past the server's blanket WriteTimeout (20s loopback) so
	// the response is never cut mid-wait — a cut connection showed up in
	// the browser as "TypeError: Failed to fetch" while provisioning
	// silently continued. net/http re-arms the original deadline for the
	// next request on the connection.
	_ = http.NewResponseController(w).SetWriteDeadline(
		time.Now().Add(s.access.waitTimeout() + 30*time.Second))
	sess := sessionFrom(r.Context())
	var req domainRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	domain := httpsd.NormalizeDomain(req.Domain)
	cfg := s.requireDomainReady(w, r, domain)
	if cfg == nil {
		return
	}

	// 1. DNS must already point at this VPS.
	resolved, expected, matches, derr := s.dnsMatchesVPS(r.Context(), cfg, domain)
	if derr != nil || !matches {
		audit(r.Context(), s.st, sess.username, "domain_apply_rejected", "access",
			map[string]any{"domain": domain, "reason": "dns_mismatch"})
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":    "dns_mismatch",
			"message":  "DNS for " + domain + " does not point at this server yet; nothing was changed.",
			"resolved": resolved,
			"expected": expected,
		})
		return
	}

	// 2. Write the overlay; the proxy worker picks it up and issues.
	if err := httpsd.SaveDomainOverlay(s.access.OverlayDir, domain); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not store the domain request")
		return
	}

	// 3. Wait for the certificate slot to go active. The IP certificate
	//    keeps serving the whole time — a failure here only ever costs
	//    the domain slot.
	ctx := r.Context()
	deadline := time.Now().Add(s.access.waitTimeout())
	var active bool
	var lastErr string
	for time.Now().Before(deadline) {
		if cerr := ctx.Err(); cerr != nil {
			break
		}
		st, err := httpsd.ReadStatus(s.access.HTTPSStateDir)
		if err == nil && st.Domain != nil && st.Domain.Host == domain {
			if st.Domain.Phase == httpsd.PhaseActive {
				active = true
				break
			}
			if st.Domain.Phase == httpsd.PhaseError && st.Domain.LastError != "" {
				lastErr = st.Domain.LastError
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.access.pollEvery()):
		}
	}

	// 4. Route verification: TLS + proxy + backend through the domain.
	if active {
		if verr := s.access.verifyRoute(ctx, domain, cfg.EffectiveTLSALPNPort()); verr != nil {
			active = false
			lastErr = verr.Error()
		}
	}

	if !active {
		// Roll the overlay back: leave the instance exactly as it was.
		_ = httpsd.SaveDomainOverlay(s.access.OverlayDir, "")
		audit(r.Context(), s.st, sess.username, "domain_apply_failed", "access",
			map[string]any{"domain": domain, "reason": firstLineOf(lastErr)})
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "domain_not_ready",
			"message": "The certificate for " + domain + " could not be issued and verified, so nothing was changed. Your server-IP access keeps working. Reason: " + firstLineOf(lastErr),
		})
		return
	}

	st, _ := httpsd.ReadStatus(s.access.HTTPSStateDir)
	notAfter := ""
	if st.Domain != nil && !st.Domain.NotAfter.IsZero() {
		notAfter = st.Domain.NotAfter.UTC().Format(time.RFC3339)
	}
	audit(r.Context(), s.st, sess.username, "domain_applied", "access",
		map[string]any{"domain": domain})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"domain":    domain,
		"not_after": notAfter,
		"url":       "https://" + domain + "/",
	})
}

type domainRemoveRequest struct {
	Confirm bool `json:"confirm"`
}

// firstLineOf renders the first line of a possibly multi-line error for
// status surfaces (same shape as the CLI helper of the same purpose).
func firstLineOf(s string) string {
	if i := indexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func (s *server) handleDomainRemove(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req domainRemoveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !req.Confirm {
		writeError(w, http.StatusBadRequest, "confirm_required",
			"removing the domain requires confirm: true")
		return
	}
	if s.access == nil || s.access.OverlayDir == "" {
		writeError(w, http.StatusServiceUnavailable, "access_unavailable",
			"the access configuration is not available to this daemon")
		return
	}
	if err := httpsd.SaveDomainOverlay(s.access.OverlayDir, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not clear the domain")
		return
	}
	audit(r.Context(), s.st, sess.username, "domain_removed", "access", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
