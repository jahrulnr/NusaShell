package transport

import (
	"io/fs"
	"net/http"
)

// StaticHandler serves the embedded frontend; in dev mode it serves the
// frontend/ directory from disk for live reload without a build step.
// Responses get Cache-Control: no-cache so the browser revalidates every
// asset and never keeps serving a stale frontend after a server upgrade.
func StaticHandler(assets fs.FS, dev bool) http.Handler {
	var fileServer http.Handler
	if dev {
		fileServer = http.FileServer(http.Dir("frontend"))
	} else {
		fileServer = http.FileServer(http.FS(assets))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}
