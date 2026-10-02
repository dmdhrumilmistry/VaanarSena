// Package web serves the embedded admin console.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

//go:embed static
var static embed.FS

// Handler serves the console. Unknown paths fall back to index.html so the
// single-page app can own client-side routes.
//
// For frontend development set VS_WEB_DIR to internal/web/static: files are
// then read from disk on every request, so edits show up on reload without
// rebuilding the binary.
func Handler() http.Handler {
	var root fs.FS
	if dir := os.Getenv("VS_WEB_DIR"); dir != "" {
		root = os.DirFS(dir)
	} else {
		root, _ = fs.Sub(static, "static")
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || !strings.Contains(p, ".") {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
