package dashboard

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildHas reports whether a prerendered page exists in the embedded
// build (tests skip gracefully when the dashboard has not been built).
func buildHas(t *testing.T, name string) bool {
	t.Helper()
	sub, err := fs.Sub(files, "build")
	if err != nil {
		t.Fatalf("sub build: %v", err)
	}
	_, err = fs.Stat(sub, name)
	return err == nil
}

// TestPrerenderedRoutesServedDirectly pins the pretty-URL mapping:
// /onboarding and /login must serve their own prerendered HTML (with the
// right <title>), not the SPA fallback (index.html).
func TestPrerenderedRoutesServedDirectly(t *testing.T) {
	cases := []struct {
		path, page, wantTitle string
		// notSPAFallback marks client-rendered shells: their prerendered
		// page has no <title> of its own (the head content is applied at
		// hydration), so the assertion is that the SPA entry — whose
		// title is "Drevanta — Server Console" — was NOT served.
		notSPAFallback bool
	}{
		{"/onboarding", "onboarding.html", "Onboarding — Drevanta", false},
		{"/login", "login.html", "Sign in — Drevanta", false},
		{"/settings", "settings.html", "Settings — Drevanta", false},
		{"/invite", "invite.html", "", true},
		{"/processes", "processes.html", "Processes — Drevanta", false},
		{"/services", "services.html", "Services — Drevanta", false},
	}
	for _, c := range cases {
		if !buildHas(t, c.page) {
			t.Skipf("web build missing %s; run npm run build", c.page)
		}
		srv := httptest.NewServer(Handler())
		resp, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Fatalf("GET %s: %v", c.path, err)
		}
		// io.ReadAll: a single Read may return a partial TCP segment
		// and truncate mid-<head> (this flaked when the <title> sat
		// just past the first segment boundary).
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		srv.Close()
		if err != nil {
			t.Fatalf("read %s: %v", c.path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", c.path, resp.StatusCode)
		}
		if c.notSPAFallback {
			if strings.Contains(string(body), "<title>Drevanta — Server Console</title>") {
				t.Fatalf("GET %s served the SPA fallback instead of %s", c.path, c.page)
			}
		} else if !strings.Contains(string(body), "<title>"+c.wantTitle+"</title>") {
			t.Fatalf("GET %s served the wrong page: title marker %q not found", c.path, c.wantTitle)
		}
	}
}

// TestUnknownPathFallsBackToSPA pins the fallback for genuinely unknown
// routes (deep client-side links).
func TestUnknownPathFallsBackToSPA(t *testing.T) {
	if !buildHas(t, "index.html") {
		t.Skip("web build missing index.html; run npm run build")
	}
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/some/future/route")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET unknown route = %d, want 200 (SPA fallback)", resp.StatusCode)
	}
}
