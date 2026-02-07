package api

import (
	"io/fs"
	"net/http"
	"strings"
)

// NewSPAHandler creates an HTTP handler that serves the embedded SPA.
// It serves static files from the embedded filesystem and falls back
// to index.html for client-side routing.
func NewSPAHandler() http.Handler {
	// Get the web/dist subdirectory from the embedded FS
	distFS, err := fs.Sub(WebAssets, "web/dist")
	if err != nil {
		// If web/dist doesn't exist (not built yet), return a placeholder handler
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Frontend not built. Run 'make frontend' first.", http.StatusNotFound)
		})
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// For API routes, let them fall through (this handler shouldn't be called for /api/)
		if strings.HasPrefix(path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the file directly
		if path != "/" {
			// Check if file exists
			cleanPath := strings.TrimPrefix(path, "/")
			if _, err := fs.Stat(distFS, cleanPath); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Fall back to index.html for SPA routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
