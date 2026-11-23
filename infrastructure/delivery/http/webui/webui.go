// Package webui is the WebUI static-asset boundary: it serves the
// embedded frontend on the same loopback origin as the API so the
// browser shares one Host/Origin envelope with HTTP and the socket.
// It owns only file delivery and response headers — no conversation,
// provider, or settings behavior lives here.
package webui

import (
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Register mounts the exact asset surfaces onto the engine. A nil
// assets FS registers nothing — the API-only composition stays valid.
// There is deliberately no SPA fallback: unknown paths 404.
func Register(r *gin.Engine, assets fs.FS) {
	if assets == nil {
		return
	}
	serve := func(c *gin.Context) {
		headers(c)
		name := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		f, err := assets.Open(name)
		if err != nil {
			transport.WriteProblem(c, http.StatusNotFound, "not_found", "no such asset")
			return
		}
		st, _ := f.Stat()
		_ = f.Close()
		if st == nil || st.IsDir() {
			transport.WriteProblem(c, http.StatusNotFound, "not_found", "no such asset")
			return
		}
		http.FileServer(http.FS(assets)).ServeHTTP(c.Writer, c.Request)
	}
	for _, p := range []string{"/", "/app.js", "/favicon.svg"} {
		r.GET(p, serve)
		r.HEAD(p, serve)
	}
	for _, p := range []string{"/styles/*path", "/lib/*path", "/components/*path", "/vendor/*path"} {
		r.GET(p, serve)
		r.HEAD(p, serve)
	}
}

// Shell answers a browser navigation the router could not match: the app
// owns its routes, so an unknown path returns the shell (with 404) and
// the app shows its own not-found view instead of a problem+json body a
// browser cannot render. API paths, missing assets, and requests that did
// not ask for HTML (fetch, curl, an <img> load) keep the envelope's
// problem+json.
func Shell(c *gin.Context, assets fs.FS) bool {
	if assets == nil || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
		return false
	}
	if strings.HasPrefix(c.Request.URL.Path, "/api/") || !wantsHTML(c.GetHeader("Accept")) {
		return false
	}
	page, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return false
	}
	headers(c)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusNotFound)
	if c.Request.Method == http.MethodHead {
		// A HEAD answer keeps the length and marks the response written,
		// or Gin appends its plain-text 404.
		c.Header("Content-Length", strconv.Itoa(len(page)))
		c.Writer.WriteHeaderNow()
		return true
	}
	_, _ = c.Writer.Write(page)
	return true
}

// wantsHTML reports a page load: a form or fetch asks for JSON or */*,
// while a navigation names text/html.
func wantsHTML(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		media, _, _ := strings.Cut(part, ";")
		if strings.EqualFold(strings.TrimSpace(media), "text/html") {
			return true
		}
	}
	return false
}

// headers pins cache and content-type behavior for asset responses.
// No Content-Security-Policy: diagram and markdown rendering need the
// styles their own markup carries.
func headers(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}
