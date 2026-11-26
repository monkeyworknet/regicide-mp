package server

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// webFS holds the UI. It is embedded so the binary is self-contained; the
// -web flag swaps in an on-disk directory for development.
//
//go:embed web
var webFS embed.FS

// staticHandler serves index.html for "/" and real files for everything else.
// Unknown paths return 404 (no SPA fallback yet; the UI is a single page but
// deep links are not part of v0).
func (s *Server) staticHandler() http.Handler {
	var files fs.FS
	if s.opts.WebDir != "" {
		files = os.DirFS(s.opts.WebDir)
	} else {
		sub, err := fs.Sub(webFS, "web")
		if err != nil {
			s.log.Printf("embed sub: %v", err)
			return http.NotFoundHandler()
		}
		files = sub
	}
	fileServer := http.FileServer(http.FS(files))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET, HEAD")
			return
		}
		p := path.Clean(r.URL.Path)
		if p == "/" {
			p = "/index.html"
		}
		// The replay viewer has its own page: a replay is read-only and must not be
		// able to touch a live session, so it does not load the game shell.
		if p == "/replay" {
			p = "/replay.html"
		}
		info, err := fs.Stat(files, strings.TrimPrefix(p, "/"))
		if err != nil || info.IsDir() {
			// Directories are not listings: /img/ and /data/ must not be browsable.
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		// HTML, JS, CSS and the registry are rebuilt with the binary: always
		// revalidate, or a browser can keep running yesterday's client against
		// today's server — or offer a retired Jester face from a cached catalogue.
		if strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css") || strings.HasSuffix(p, ".svg") || strings.HasSuffix(p, ".json") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Assets are content-addressed by build, so they may be cached briefly.
		if strings.HasPrefix(p, "/fonts/") || strings.HasPrefix(p, "/sfx/") || strings.HasPrefix(p, "/img/") {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		// Serve the path the handler decided on when it had to rewrite one (/replay is
		// a page, not a directory), otherwise the request as it arrived: the file
		// server redirects /index.html back to "/", so rewriting "/" would loop.
		if p != path.Clean(r.URL.Path) && p != "/index.html" {
			out := r.Clone(r.Context())
			out.URL.Path = p
			fileServer.ServeHTTP(w, out)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
