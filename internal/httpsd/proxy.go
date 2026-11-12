package httpsd

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const http01PathPrefix = "/.well-known/acme-challenge/"

// Proxy is the public-facing layer: :80 (challenge + strict redirect)
// and :443 (TLS termination + reverse proxy to the loopback backend).
type Proxy struct {
	cfg Config
	mgr *Manager
}

func NewProxy(cfg Config, mgr *Manager) *Proxy {
	return &Proxy{cfg: cfg, mgr: mgr}
}

// BackendURL is the fixed private upstream. The backend is loopback-only
// by design; a non-loopback backend in the config is refused.
func backendURL(backend string) (*url.URL, error) {
	host, port, err := net.SplitHostPort(backend)
	if err != nil {
		return nil, errors.New("backend must be host:port")
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsLoopback() && ip.To4() != nil && !ip.IsUnspecified()) {
		// Only loopback is acceptable; IPv6 loopback (::1) parses as IsLoopback.
		if !(ip != nil && ip.IsLoopback()) {
			return nil, errors.New("backend must be a loopback address (127.0.0.1)")
		}
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)}, nil
}

// HTTPHandler serves port 80: ACME http-01 responses only, everything
// else is redirected to HTTPS. There is no plain-HTTP path to the
// backend — ever.
func (p *Proxy) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, http01PathPrefix) {
			token := strings.TrimPrefix(r.URL.Path, http01PathPrefix)
			if keyAuth, ok := p.mgr.HTTP01ChallengeValue(token); ok {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(keyAuth))
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
			host = h
		}
		// Supply the port only when it is not the implicit default.
		location := "https://" + host
		if p.cfg.EffectiveTLSALPNPort() != 443 {
			location += ":" + strconv.Itoa(p.cfg.EffectiveTLSALPNPort())
		}
		location += r.URL.RequestURI()
		w.Header().Set("Location", location)
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusPermanentRedirect)
	})
}

// HTTPSHandler terminates TLS: strict forwarding headers, HSTS, then the
// loopback backend. The public Host header is passed through unchanged
// so the backend's same-origin (CSRF) checks keep seeing the address the
// browser actually used.
func (p *Proxy) HTTPSHandler() (http.Handler, error) {
	target, err := backendURL(p.cfg.Backend)
	if err != nil {
		return nil, err
	}
	rp := &httputil.ReverseProxy{
		FlushInterval: 250 * time.Millisecond,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("httpsd: backend unavailable", "err", err)
			http.Error(w, "Drevanta backend is unreachable", http.StatusBadGateway)
		},
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Preserve the browser's Host (IP or custom domain): the
			// backend validates Origin/Referer against it for CSRF
			// protection.
			pr.Out.Host = pr.In.Host

			// Trusted-proxy boundary: the browser never gets to decide
			// its own forwarding identity. Strip everything inbound, then
			// set exactly one hop of truth.
			h := pr.Out.Header
			for _, k := range []string{
				"Forwarded", "X-Forwarded-For", "X-Forwarded-Host",
				"X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-IP",
			} {
				h.Del(k)
			}
			clientIP, _, splitErr := net.SplitHostPort(pr.In.RemoteAddr)
			if splitErr != nil {
				clientIP = pr.In.RemoteAddr
			}
			h.Set("X-Forwarded-For", clientIP)
			h.Set("X-Forwarded-Proto", "https")
			h.Set("X-Forwarded-Host", pr.In.Host)
			h.Set("X-Forwarded-Port", strconv.Itoa(p.cfg.EffectiveTLSALPNPort()))
			h.Set("X-Real-IP", clientIP)
		},
	}
	// applyDeadlineWrapper extends the write deadline for the domain-apply
	// route: the backend legitimately holds that response open for the
	// whole certificate wait (up to the 90s wait timeout), which exceeds
	// this server's blanket 60s WriteTimeout on the HTTP/1.1 edge.
	// (HTTP/2 connections are not subject to WriteTimeout in net/http.)
	// net/http re-arms the original deadline for the next request.
	applyDeadline := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/access/domain/apply" {
				_ = http.NewResponseController(w).SetWriteDeadline(
					time.Now().Add(150 * time.Second))
			}
			next.ServeHTTP(w, r)
		})
	}
	return securityHeaders(applyDeadline(rp)), nil
}

// securityHeaders adds the HTTPS-only response headers. Everything is
// also set by the daemon itself (httpapi) for tunnel mode; the proxy
// repeats them so HTTPS responses are covered even where the backend
// headers are rewritten.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; "+
				"base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r)
	})
}

// TLSConfig builds the :443 TLS configuration: ACME tls-alpn-01 hook
// plus the currently served certificate, modern protocol floor.
// "acme-tls/1" must be advertised: Go aborts handshakes whose client
// ALPN has no overlap with the server list, which would break the
// RFC 8737 challenge response.
func (p *Proxy) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: p.mgr.ChallengeHook(),
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1", "acme-tls/1"},
	}
}

// Serve runs both public listeners until ctx is cancelled. Port 443
// answers handshakes only once a certificate exists (or during the ACME
// tls-alpn-01 challenge itself) — no certificate, no TLS service.
func Serve(ctx context.Context, cfg Config, mgr *Manager) error {
	p := NewProxy(cfg, mgr)
	httpsHandler, err := p.HTTPSHandler()
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindIP, strconv.Itoa(cfg.EffectiveHTTP01Port())),
		Handler:           p.HTTPHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	tlsSrv := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindIP, strconv.Itoa(cfg.EffectiveTLSALPNPort())),
		Handler:           httpsHandler,
		TLSConfig:         p.TLSConfig(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		slog.Info("httpsd: http listener up (challenge + redirect)", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	go func() {
		slog.Info("httpsd: https listener up", "addr", tlsSrv.Addr)
		// Certificates come from GetCertificate (memory), not files.
		if err := tlsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	shutdown := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
		_ = tlsSrv.Shutdown(sctx)
	}
	select {
	case <-ctx.Done():
		shutdown()
		return nil
	case err := <-errCh:
		shutdown()
		return err
	}
}
