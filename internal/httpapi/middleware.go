// Session middleware and request guards.
//
// Threat model: the daemon binds to 127.0.0.1 and is reached through an
// SSH tunnel, so the network itself is already operator-only. The guards
// below defend the browser surface against the remaining classic web
// risks (session theft via XSS-adjacent vectors, cross-site request
// forgery, brute force) without adding dependencies.
package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// Session cookie and lifetime policy.
const (
	sessionCookie   = "drevanta_session"
	sessionSliding  = 24 * time.Hour // refreshed on every authenticated request
	sessionAbsolute = 7 * 24 * time.Hour
)

// session is the resolved identity attached to a request.
type session struct {
	raw      string // cookie value (plaintext token); only in memory
	rec      store.Session
	username string
	mfaOn    bool
}

// resolveSession validates the request cookie against the store. Expired
// or unknown cookies yield nil, not an error — anonymous is a valid state.
func (s *server) resolveSession(r *http.Request) (*session, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	hash := auth.TokenHash(c.Value)
	rec, err := s.st.SessionByHash(r.Context(), hash)
	if err != nil {
		return nil, nil // unknown/expired: treat as anonymous
	}
	u, err := s.st.UserByID(r.Context(), rec.UserID)
	if err != nil {
		return nil, nil
	}
	return &session{raw: c.Value, rec: rec, username: u.Username, mfaOn: u.MFAEnabled}, nil
}

// touch slides the session window forward for authenticated requests.
func (s *server) touch(sess *session) {
	_ = s.st.TouchSession(context.Background(), auth.TokenHash(sess.raw), sessionSliding)
}

// guard wraps state-changing (POST) endpoints with the same-origin check.
func (s *server) guard(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross_origin", "request origin rejected")
			return
		}
		next(w, r)
	})
}

// sameOrigin enforces that browser-issued state-changing requests carry a
// Origin/Referer matching the request host when either header is present.
// Combined with SameSite=Lax cookies this closes CSRF for the tunnel-only
// deployment; non-browser clients (curl) send neither header and pass.
func sameOrigin(r *http.Request) bool {
	if !isUnsafeMethod(r.Method) {
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.Host != r.Host {
			return false
		}
		return true
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil || u.Host == "" || u.Host != r.Host {
			return false
		}
	}
	return true
}

func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// requireSession resolves a session and enforces a minimum scope.
func (s *server) requireSession(minScope string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.resolveSession(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return
		}
		if sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		if minScope == store.ScopeFull && sess.rec.Scope != store.ScopeFull {
			writeError(w, http.StatusForbidden, "insufficient_scope", "onboarding must be completed first")
			return
		}
		s.touch(sess)
		next(w, r.WithContext(withSession(r.Context(), sess)))
	})
}

// requireSessionHandler adapts requireSession for use inside srv.guard.
func (s *server) requireSessionHandler(minScope string, next http.HandlerFunc) http.HandlerFunc {
	h := s.requireSession(minScope, next)
	return h.ServeHTTP
}

// withOptionalSession resolves a session when one is presented and attaches
// it to the request context, but does not require one. For endpoints that
// serve both anonymous and signed-in flows (invitation acceptance): a
// signed-in invitee joins with their existing account, everyone else gets
// the create-account path.
func (s *server) withOptionalSession(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.resolveSession(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return
		}
		if sess != nil {
			s.touch(sess)
			next(w, r.WithContext(withSession(r.Context(), sess)))
			return
		}
		next(w, r)
	})
}

// requireFullSession is the metrics/dashboard gate.
func (s *server) requireFullSession(next http.HandlerFunc) http.Handler {
	return s.requireSession(store.ScopeFull, next)
}

// securityHeaders pins the browser-facing hardening headers on every
// response, in both access modes (public HTTPS proxy and SSH tunnel).
// CSP allows the SvelteKit boot inline script (no nonces available in a
// static-adapter app) but pins everything else to 'self', forbids
// framing and plugin objects, and locks down base-uri/form-action.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; "+
				"base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if requestIsHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the rate-limit bucket for a request.
//
// Behind the public HTTPS proxy every request arrives from 127.0.0.1, so
// the raw RemoteAddr would put ALL browser clients into one bucket — a
// single attacker could lock out every other user. When (and only when)
// the connection is loopback, the proxy-set X-Forwarded-For value is
// used instead. The proxy overwrites inbound forwarding headers, so
// this value is one hop of truth; a local process could still forge it,
// but that already implies local compromise (same trust level as the
// SSH-tunnel deployment).
func clientKey(r *http.Request, kind string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if net.ParseIP(host).IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if client, _, perr := net.SplitHostPort(xff); perr == nil && client != "" {
				host = client // "ip:port" form
			} else if ip := net.ParseIP(strings.TrimSpace(xff)); ip != nil {
				host = ip.String()
			}
		}
	}
	return kind + ":" + host
}
