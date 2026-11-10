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
		if _, err := fs.Stat(sub, p); err != nil {
			// Unknown path: serve the SPA entry point.
			r.URL.Path = "/"
		} else if strings.HasPrefix(p, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}
