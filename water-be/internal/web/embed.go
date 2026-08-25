package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Dist contains the production frontend copied here by the release/build script.
// The tracked placeholder keeps backend-only tests and development builds valid.
//
//go:embed dist
var Dist embed.FS

// Handler serves the embedded Vue application and falls back to index.html for
// client-side routes. API and WebSocket paths are handled by the parent router.
func Handler() http.Handler {
	assets, err := fs.Sub(Dist, "dist")
	if err != nil {
		return http.HandlerFunc(unavailableHandler)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		requestPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if requestPath == "." || requestPath == "" {
			requestPath = "index.html"
		}

		if info, statErr := fs.Stat(assets, requestPath); statErr == nil && !info.IsDir() {
			serveFile(w, r, assets, requestPath)
			return
		}

		if _, statErr := fs.Stat(assets, "index.html"); statErr != nil {
			unavailableHandler(w, r)
			return
		}

		serveFile(w, r, assets, "index.html")
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, assets fs.FS, name string) {
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, assets, name)
}

func unavailableHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "frontend assets are not embedded; run scripts/build-single-binary.sh", http.StatusNotImplemented)
}
