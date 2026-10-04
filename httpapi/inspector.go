package httpapi

import (
	"embed"
	"net/http"
)

//go:embed ui/index.html ui/app.js ui/model.mjs ui/graph.mjs ui/sequence.mjs ui/tree.mjs ui/style.css
var inspectorAssets embed.FS

// The public shell contains no state or credential. Data requests use the same
// authenticated API as CLI clients; the inspector never acknowledges messages.
func inspectorHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var path, mime string
		switch r.URL.Path {
		case "/ui/", "/ui/index.html":
			path, mime = "ui/index.html", "text/html; charset=utf-8"
		case "/ui/app.js":
			path, mime = "ui/app.js", "text/javascript; charset=utf-8"
		case "/ui/model.mjs":
			path, mime = "ui/model.mjs", "text/javascript; charset=utf-8"
		case "/ui/graph.mjs":
			path, mime = "ui/graph.mjs", "text/javascript; charset=utf-8"
		case "/ui/sequence.mjs":
			path, mime = "ui/sequence.mjs", "text/javascript; charset=utf-8"
		case "/ui/tree.mjs":
			path, mime = "ui/tree.mjs", "text/javascript; charset=utf-8"
		case "/ui/style.css":
			path, mime = "ui/style.css", "text/css; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		body, err := inspectorAssets.ReadFile(path)
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", mime)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}
