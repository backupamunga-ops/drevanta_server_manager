// Package httpapi wires the frozen Sprint 1 API surface, the Sprint 2
// identity/onboarding endpoints, and the embedded dashboard into one
// loopback-only HTTP handler.
//
// API surface:
//
//	GET  /healthz                          — liveness (unauthenticated)
//	GET  /readyz                           — readiness (unauthenticated)
//	GET  /api/v1/server/metrics            — on-demand snapshot (session, full scope)
//	GET  /api/v1/auth/session              — current session state
//	POST /api/v1/auth/login                — login (TOTP / recovery code when MFA on)
//	POST /api/v1/auth/logout               — logout
//	GET  /api/v1/onboarding/status         — first-owner onboarding state
//	POST /api/v1/onboarding/claim          — claim token + administrator creation
//	POST /api/v1/onboarding/mfa/setup      — begin TOTP enrolment
//	POST /api/v1/onboarding/mfa/verify     — confirm TOTP, issue recovery codes
//	POST /api/v1/onboarding/mfa/skip       — explicit MFA skip (risk acknowledged)
//	GET  /                                 — embedded SvelteKit dashboard
//
// /healthz and /readyz stay unauthenticated by design (systemd and
// monitoring need them and they reveal nothing); every other /api route
// requires a session, and metrics additionally requires full scope.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/auth"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/metrics"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
	dashboard "github.com/backupamunga-ops/drevanta_server_manager/web"
)

// server carries the shared dependencies of every handler.
type server struct {
	st         *store.Store
	version    string
	collector  *metrics.Collector
	limiter    *auth.RateLimiter
	argonSlots chan struct{}
}

// argonConcurrency bounds how many password verifications run at once.
// Each argon2id verification allocates a ~19 MiB workspace; without a
// bound, a burst of concurrent logins could stack ~190 MiB against the
// daemon's 256 MiB ceiling. Two slots cap that at ~38 MiB + base; the
// third concurrent attempt gets 503 (loopback-only surface, rate limited
// anyway, so this is a memory-safety backstop, not a load feature).
const argonConcurrency = 2

// New builds the full HTTP handler.
func New(st *store.Store, version string, diskPaths []string) http.Handler {
	srv := &server{
		st:         st,
		version:    version,
		collector:  metrics.NewCollector(diskPaths),
		limiter:    auth.NewRateLimiter(10, time.Minute),
		argonSlots: make(chan struct{}, argonConcurrency),
	}
	mux := http.NewServeMux()

	// --- Frozen Sprint 1 surface ---
	mux.HandleFunc("GET /healthz", srv.handleHealthz)
	mux.HandleFunc("GET /readyz", srv.handleReadyz)
	mux.Handle("GET /api/v1/server/metrics", srv.requireFullSession(srv.handleMetrics))

	// --- Sprint 2 identity / onboarding ---
	mux.HandleFunc("GET /api/v1/auth/session", srv.handleSessionInfo)
	mux.Handle("POST /api/v1/auth/login", srv.guard(srv.handleLogin))
	mux.Handle("POST /api/v1/auth/logout", srv.guard(srv.handleLogout))
	mux.HandleFunc("GET /api/v1/onboarding/status", srv.handleOnboardingStatus)
	mux.Handle("POST /api/v1/onboarding/claim", srv.guard(srv.handleClaim))
	mux.Handle("POST /api/v1/onboarding/mfa/setup", srv.guard(srv.requireSessionHandler(store.ScopeOnboarding, srv.handleMFASetup)))
	mux.Handle("POST /api/v1/onboarding/mfa/verify", srv.guard(srv.requireSessionHandler(store.ScopeOnboarding, srv.handleMFAVerify)))
	mux.Handle("POST /api/v1/onboarding/mfa/skip", srv.guard(srv.requireSessionHandler(store.ScopeOnboarding, srv.handleMFASkip)))

	// Strict API namespace: unknown /api/* paths are 404 JSON, never the
	// SPA fallback.
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	mux.HandleFunc("POST /api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})

	mux.Handle("GET /", dashboard.Handler())

	return recoverAndLog(mux)
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Ready(r.Context()); err != nil {
		slog.Warn("readiness check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	snap, err := s.collector.Snapshot()
	if err != nil {
		slog.Error("metrics collection failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "metrics collection failed"})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits the standard error envelope {"error","message"}.
func writeError(w http.ResponseWriter, code int, errCode, message string) {
	writeJSON(w, code, map[string]string{"error": errCode, "message": message})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// recoverAndLog adds panic recovery and error-focused request logging.
// Successful requests are logged at debug level so the 5-second dashboard
// polling does not flood journald.
func recoverAndLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic in handler", "path", r.URL.Path, "panic", p)
				writeJSON(rec, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			if rec.status >= http.StatusBadRequest {
				slog.Warn("http request",
					"method", r.Method, "path", r.URL.Path,
					"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
			} else {
				slog.Debug("http request",
					"method", r.Method, "path", r.URL.Path,
					"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
			}
		}()
		next.ServeHTTP(rec, r)
	})
}
