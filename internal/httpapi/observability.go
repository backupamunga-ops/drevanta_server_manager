// P2.1 observability endpoints (RFC docs/phase-2/P2.1-observability-rfc.md):
// overview, history, processes, services, alerts, and the first
// preflight. Every route here is READ-ONLY — mutations arrive with the
// P2.2 privileged helper — and every route re-derives authorization
// per request: server workspace context + the P2.0 registry permission
// (system.overview.read, system.metrics.read, system.processes.read,
// system.services.read).
package httpapi

import (
	"net/http"
	"runtime"
	"time"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/metrics"
	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// requireServerPerm gates a server-scoped endpoint on the caller being
// IN the server workspace context (P2.0: switching away hides the
// server surface) AND holding perm on the server workspace per the
// registry. This supersedes the bare scope gate for metrics: a viewer
// role with a full-scope session must NOT read what its role denies.
func (s *server) requireServerPerm(perm string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess.rec.ActiveWorkspaceID != 0 {
			serverWS, err := s.st.ServerWorkspace(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", "workspace resolution failed")
				return
			}
			if sess.rec.ActiveWorkspaceID != serverWS.ID {
				writeError(w, http.StatusForbidden, "workspace_mismatch",
					"the server surface is not available in the current workspace context")
				return
			}
		}
		serverWS, err := s.st.ServerWorkspace(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "workspace resolution failed")
			return
		}
		allowed, err := s.st.Can(r.Context(), sess.rec.UserID, serverWS.ID, perm)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "authorization check failed")
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "forbidden", "your role does not permit this action")
			return
		}
		next(w, r)
	})
}

// ---------------------------------------------------------------------------
// Overview (one call the console renders on load — RFC D-03)
// ---------------------------------------------------------------------------

func (s *server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := s.collector.Snapshot()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "metrics_unavailable", err.Error())
		return
	}
	history, err := s.st.MetricHistory(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		history = nil // overview degrades honestly without history
	}
	alerts, err := s.st.ListAlerts(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		alerts = nil
	}
	units, err := metrics.Services()
	if err != nil {
		units = nil
	}
	worst := 0.0
	worstPath := ""
	for _, d := range snap.Disk {
		if d.UsedPercent > worst {
			worst = d.UsedPercent
			worstPath = d.Path
		}
	}
	openAlerts := []map[string]any{}
	for _, a := range alerts {
		if a.State == "open" {
			openAlerts = append(openAlerts, alertView(a))
		}
	}
	summary := metrics.SystemdSummaryOf(units)
	writeJSON(w, http.StatusOK, map[string]any{
		"snapshot":   snap,
		"history":    history,
		"alerts":     openAlerts,
		"systemd":    summary,
		"worst_disk": worst,
		"worst_path": worstPath,
	})
}

func alertView(a store.HealthAlert) map[string]any {
	return map[string]any{
		"id":            a.ID,
		"kind":          a.Kind,
		"subject":       a.Subject,
		"severity":      a.Severity,
		"message":       a.Message,
		"value":         a.Value,
		"threshold":     a.Threshold,
		"state":         a.State,
		"first_seen_at": a.FirstSeenAt.UTC().Format(time.RFC3339),
		"last_seen_at":  a.LastSeenAt.UTC().Format(time.RFC3339),
		"resolved_at":   orEmpty(a.ResolvedAt),
	}
}

func orEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ---------------------------------------------------------------------------
// History (metric history with bounded retention, RFC D-03)
// ---------------------------------------------------------------------------

var historyWindows = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"14d": 14 * 24 * time.Hour,
}

func (s *server) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	d, ok := historyWindows[window]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"window must be one of: 1h, 24h, 7d, 14d")
		return
	}
	points, err := s.st.MetricHistory(r.Context(), time.Now().UTC().Add(-d))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "history read failed")
		return
	}
	if points == nil {
		points = []store.HistoryPoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"window": window, "points": points})
}

// ---------------------------------------------------------------------------
// Processes (computed on request, never stored — RFC D-05)
// ---------------------------------------------------------------------------

func (s *server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "linux" {
		writeError(w, http.StatusServiceUnavailable, "unsupported_platform",
			"the process list reads /proc, which only exists on Linux")
		return
	}
	procs, err := metrics.Processes(50)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "processes_unavailable", err.Error())
		return
	}
	if procs == nil {
		procs = []metrics.ProcInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"measured_over_ms": int(metrics.MeasuredOverMs()),
		"processes":        procs,
	})
}

// ---------------------------------------------------------------------------
// Services (two systemctl list calls, no mutations — RFC D-06)
// ---------------------------------------------------------------------------

func (s *server) handleServices(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "linux" {
		writeError(w, http.StatusServiceUnavailable, "unsupported_platform",
			"service health reads systemd, which is not available here")
		return
	}
	units, err := metrics.Services()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "services_unavailable",
			"systemctl could not be queried: "+err.Error())
		return
	}
	if units == nil {
		units = []metrics.ServiceUnit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": metrics.SystemdSummaryOf(units),
		"units":   units,
	})
}

// ---------------------------------------------------------------------------
// Alerts
// ---------------------------------------------------------------------------

func (s *server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, err := s.st.ListAlerts(r.Context(), time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "alert read failed")
		return
	}
	out := make([]map[string]any, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, alertView(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
}

// ---------------------------------------------------------------------------
// First preflight (RFC D-07): one stable envelope, check list grows
// per kind. Read-only by construction.
// ---------------------------------------------------------------------------

func (s *server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	const defaultKind = "package_upgrade"
	if kind == "" {
		kind = defaultKind
	}
	known := false
	for _, k := range []string{defaultKind} {
		if k == kind {
			known = true
			break
		}
	}
	if !known {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"unknown preflight kind; known kinds: package_upgrade")
		return
	}
	snap, err := s.collector.Snapshot()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "metrics_unavailable", err.Error())
		return
	}
	checks := make([]map[string]any, 0, 8)
	ok := true
	add := func(name string, pass bool, value, threshold float64, message string) {
		if !pass {
			ok = false
		}
		checks = append(checks, map[string]any{
			"name": name, "ok": pass, "value": value,
			"threshold": threshold, "message": message,
		})
	}
	// Disk headroom: >= 15% free on every monitored mount.
	for _, d := range snap.Disk {
		freePct := 100 - d.UsedPercent
		add("disk_headroom:"+d.Path, freePct >= 15, freePct, 15,
			"at least 15% free space required")
	}
	// Memory headroom: >= 300 MB available.
	availMB := float64(snap.Memory.AvailableBytes) / (1024 * 1024)
	add("memory_headroom", availMB >= 300, availMB, 300,
		"at least 300 MB available memory required")
	// Load level: load5 below core count.
	add("load_level", snap.CPU.Load5 < float64(snap.CPU.Cores),
		snap.CPU.Load5, float64(snap.CPU.Cores), "load5 below core count")
	// No active critical alerts.
	alerts, _ := s.st.ListAlerts(r.Context(), time.Now().UTC().Add(-24*time.Hour))
	crit := 0
	for _, a := range alerts {
		if a.State == "open" && a.Severity == "crit" {
			crit++
		}
	}
	add("no_critical_alerts", crit == 0, float64(crit), 0,
		"no active critical alerts allowed")
	writeJSON(w, http.StatusOK, map[string]any{"kind": kind, "ok": ok, "checks": checks})
}
