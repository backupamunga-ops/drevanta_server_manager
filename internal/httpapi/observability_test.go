package httpapi

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestObservabilityAPIAccessMatrix pins the read-only surface's
// authorization (RFC D-03..D-07): anonymous 401; full-scope session in
// the server context 200; org context 403 workspace_mismatch; unknown
// preflight kind 400. Services/processes need Linux.
func TestObservabilityAPIAccessMatrix(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAdmin(t, h, st, "owner")
	orgID := int64(0)

	// Anonymous: every observability route is 401.
	for _, path := range []string{
		"/api/v1/server/overview", "/api/v1/server/metrics/history",
		"/api/v1/server/processes", "/api/v1/server/services",
		"/api/v1/server/alerts", "/api/v1/server/preflight",
	} {
		rec := do(t, h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous GET %s = %d, want 401", path, rec.Code)
		}
	}

	// Owner in the server context: overview + alerts + preflight 200
	// everywhere; history 200 (empty until the sampler has run a while —
	// the tests call the endpoints directly, no sampler running).
	for _, path := range []string{
		"/api/v1/server/overview", "/api/v1/server/metrics/history?window=24h",
		"/api/v1/server/alerts", "/api/v1/server/preflight?kind=package_upgrade",
	} {
		rec := do(t, h, http.MethodGet, path, "", hdr)
		if rec.Code != http.StatusOK {
			t.Fatalf("owner GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	// Overview must carry the live snapshot shape.
	m := decodeMap(t, do(t, h, http.MethodGet, "/api/v1/server/overview", "", hdr))
	if _, ok := m["snapshot"]; !ok {
		t.Fatalf("overview missing snapshot: %v", m)
	}
	if _, ok := m["systemd"]; !ok {
		t.Fatalf("overview missing systemd rollup: %v", m)
	}

	// Unknown history window and preflight kind are 400.
	rec := do(t, h, http.MethodGet, "/api/v1/server/metrics/history?window=99y", "", hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad history window = %d, want 400", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/server/preflight?kind=format_the_disk", "", hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad preflight kind = %d, want 400", rec.Code)
	}

	// Processes + services are Linux-only.
	if runtime.GOOS == "linux" {
		rec = do(t, h, http.MethodGet, "/api/v1/server/processes", "", hdr)
		if rec.Code != http.StatusOK {
			t.Fatalf("owner processes = %d: %s", rec.Code, rec.Body.String())
		}
		if !containsJSON(t, rec.Body.String(), `"measured_over_ms"`) {
			t.Fatal("processes response missing the measurement-window disclosure")
		}
		rec = do(t, h, http.MethodGet, "/api/v1/server/services", "", hdr)
		switch rec.Code {
		case http.StatusOK:
			// systemd available: the rollup must be present
			if !containsJSON(t, rec.Body.String(), `"summary"`) {
				t.Fatal("services response missing the systemd rollup")
			}
		case http.StatusServiceUnavailable:
			// No systemd in this container: the honest degradation.
			if errCode(decodeMap(t, rec)) != "services_unavailable" {
				t.Fatalf("services 503 body: %s", rec.Body.String())
			}
		default:
			t.Fatalf("owner services = %d: %s", rec.Code, rec.Body.String())
		}
	}

	// Create an org and switch into it: the observability surface must
	// answer 403 workspace_mismatch (P2.0 rule 6 — switching never
	// grants, and the server surface is not available in org context).
	rec = do(t, h, http.MethodPost, "/api/v1/workspaces",
		`{"name":"Acme Hosting","business_type":"agency"}`, hdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org = %d: %s", rec.Code, rec.Body.String())
	}
	orgID = int64(decodeMap(t, rec)["workspace"].(map[string]any)["id"].(float64))
	rec = do(t, h, http.MethodPost, fmt.Sprintf("/api/v1/workspaces/%d/enter", orgID), "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("enter org = %d", rec.Code)
	}
	for _, path := range []string{
		"/api/v1/server/overview", "/api/v1/server/processes",
		"/api/v1/server/services", "/api/v1/server/preflight",
	} {
		rec = do(t, h, http.MethodGet, path, "", hdr)
		if rec.Code != http.StatusForbidden || errCode(decodeMap(t, rec)) != "workspace_mismatch" {
			t.Fatalf("org-context GET %s = %d (%s), want 403 workspace_mismatch",
				path, rec.Code, rec.Body.String())
		}
	}
}

func containsJSON(t *testing.T, body, marker string) bool {
	t.Helper()
	return strings.Contains(body, marker)
}

// TestAlertsRoundTrip verifies the alert API reflects lifecycle rows
// written directly through the sampler against the same store.
func TestAlertsRoundTrip(t *testing.T) {
	h, st := newTestHandler(t)
	hdr := claimAdmin(t, h, st, "owner")

	// No alerts yet: an honest empty list.
	rec := do(t, h, http.MethodGet, "/api/v1/server/alerts", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("alerts = %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); containsJSON(t, body, `"severity"`) {
		t.Fatalf("expected no alerts yet: %s", body)
	}

	// Open an alert via the store (as the sampler would) and see it on
	// the API with kind/subject/severity intact.
	if err := st.UpsertAlert(t.Context(), "disk", "/tmp", "warn",
		"filesystem usage", 81.5, 80, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, do(t, h, http.MethodGet, "/api/v1/server/alerts", "", hdr))
	list := m["alerts"].([]any)
	if len(list) != 1 {
		t.Fatalf("alerts = %d, want 1", len(list))
	}
	a := list[0].(map[string]any)
	if a["kind"] != "disk" || a["subject"] != "/tmp" || a["severity"] != "warn" || a["state"] != "open" {
		t.Fatalf("alert view wrong: %v", a)
	}
}
