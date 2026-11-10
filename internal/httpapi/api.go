// Package httpapi wires the frozen Sprint 1 API surface and the embedded
// dashboard into one loopback-only HTTP handler.
//
// Frozen API surface (Sprint 1):
//
//	GET /healthz                — liveness
//	GET /readyz                 — readiness (store reachable, migrations applied)
//	GET /api/v1/server/metrics  — on-demand CPU/RAM/disk snapshot
//	GET /                       — embedded SvelteKit dashboard
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/metrics"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
	dashboard "github.com/backupamunga-ops/drevanta_server_manager/web"
)

// New builds the full HTTP handler.
func New(st *store.Store, version string, diskPaths []string) http.Handler {
	collector := metrics.NewCollector(diskPaths)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Ready(r.Context()); err != nil {
			slog.Warn("readiness check failed", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.HandleFunc("GET /api/v1/server/metrics", func(w http.ResponseWriter, r *http.Request) {
		snap, err := collector.Snapshot()
		if err != nil {
			slog.Error("metrics collection failed", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "metrics collection failed"})
			return
		}
		writeJSON(w, http.StatusOK, snap)
	})

	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		// Strict API namespace: unknown /api/* paths are 404 JSON, never
		// the SPA fallback.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})

	mux.Handle("GET /", dashboard.Handler())

	return recoverAndLog(mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
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
