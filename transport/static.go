package transport

import (
	"io/fs"
	"net/http"
)

// StaticHandler serves the embedded frontend; in dev mode it serves the
// frontend/ directory from disk for live reload without a build step.
func StaticHandler(assets fs.FS, dev bool) http.Handler {
	if dev {
		return http.FileServer(http.Dir("frontend"))
	}
	return http.FileServer(http.FS(assets))
}
