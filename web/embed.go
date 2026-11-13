// Package dashboard serves the SvelteKit static build embedded into the
// drevantad binary via go:embed. Nothing is fetched from CDNs; the whole
// console ships inside the single static binary.
//
// This package lives beside web/build so the go:embed pattern is
// package-relative and works without any copy step: `npm run build`
// writes web/build, and this package embeds it directly.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:build
var files embed.FS

// prettyPages maps client-side route paths to their prerendered SvelteKit
// pages, so /onboarding, /login and /settings serve the real page instead
// of the SPA fallback (the client router would resolve them anyway;
// serving the prerendered HTML is one less hydration hop and keeps
// tunnel-side checks like curl truthful).
var prettyPages = map[string]string{
	"onboarding": "onboarding.html",
	"login":      "login.html",
	"settings":   "settings.html",
	"invite":     "invite.html",
}

// Handler serves the embedded static dashboard with an index.html
// fallback for client-side routes and long-lived cache headers for
// SvelteKit's immutable hashed assets.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "build")
	if err != nil {
		// build/ is embedded at compile time; a missing subtree is a
		// build error, not a runtime condition.
		panic("dashboard: embed build/ missing: " + err.Error())
	}
	fileServer := http.FileServerFS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if mapped, ok := prettyPages[p]; ok {
			if _, err := fs.Stat(sub, mapped); err == nil {
				p = mapped
				r.URL.Path = "/" + mapped
			}
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// Unknown path: serve the SPA entry point. The shell must never
			// be cached — a stale shell references old content-hashed
			// chunks and breaks hydration after every upgrade.
			r.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(p, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else if strings.HasSuffix(p, ".html") {
			// Prerendered route shells reference content-hashed chunks:
			// never let a browser reuse a stale shell (it would keep
			// serving the previous release's UI until a hard refresh).
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
		} else {
			// Remaining assets (version.json, favicon, ...): revalidate.
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
