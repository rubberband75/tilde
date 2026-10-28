package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// Go's own table has no .woff2 (the bundled font, D56); hosts without
// /etc/mime.types would serve it as text.
func init() { _ = mime.AddExtensionType(".woff2", "font/woff2") }

const notBuiltHTML = `<!doctype html><meta charset="utf-8"><title>Tilde</title>
<body style="background:#222226;color:#e1e1e1;font-family:sans-serif;display:grid;place-items:center;height:100vh;margin:0">
<p>The Tilde frontend is not built into this binary. Run <code>make build</code>,
or use <code>make dev</code> and open the Vite dev server.</p></body>`

// spaHandler serves the built frontend. Unknown paths fall back to
// index.html; hashed files under /assets/ are cached forever.
func spaHandler(files fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(files))
	_, err := fs.Stat(files, "index.html")
	built := err == nil
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(notBuiltHTML))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(files, p); err != nil {
			p = "index.html"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if p == "index.html" {
			// Serve the app shell directly for any client-side route.
			http.ServeFileFS(w, r, files, "index.html")
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + p
		fileServer.ServeHTTP(w, r2)
	})
}
