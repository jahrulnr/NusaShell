package transport

import (
	"net/http"
	"path/filepath"
	"strings"
)

// handleLocalFile serves a local file by absolute path, proxying it
// through the HTTP server so the frontend can load file:// resources
// (images, videos) that browsers block from http:// origins.
//
// Query parameter: path=<absolute filesystem path>
//
// The route is intentionally open (any readable absolute path) — the
// product runs locally on 127.0.0.1 and the user is responsible for
// what the AI model references. This mirrors how desktop AI tools
// (Cursor, VS Code extensions) handle local file access.
func (s *Server) handleLocalFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path parameter required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(path) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}
	// Block path traversal via query string (path is already absolute,
	// but reject if it contains ../ after cleaning).
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, clean)
}
