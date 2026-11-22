// Package webui serves the embedded single-page application built from web/
// (Vite writes its output to internal/webui/dist).
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

const notBuilt = `<!doctype html><html><head><meta charset="utf-8"><title>Tossbox</title></head>
<body style="font-family:sans-serif;max-width:40rem;margin:4rem auto">
<h1>Tossbox</h1><p>The web UI was not built into this binary. Run <code>npm ci &amp;&amp; npm run build</code> in <code>web/</code>
and rebuild, or use the Docker image. The API is available under <code>/api</code>.</p></body></html>`

// Handler serves static files; unknown paths get index.html so client-side
// routes work. Hashed assets under /assets are cached for a year.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	index, err := fs.ReadFile(sub, "index.html")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil { // UI not built
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(notBuilt))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, statErr := fs.Stat(sub, p); statErr == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if path.Ext(p) != "" && !strings.HasSuffix(p, ".html") {
				http.NotFound(w, r) // missing asset: don't answer with HTML
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
