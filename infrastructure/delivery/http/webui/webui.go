// Package webui is the WebUI static-asset boundary: it serves the
// embedded frontend on the same loopback origin as the API so the
// browser shares one Host/Origin envelope with HTTP and the socket.
// It owns only file delivery and response headers — no conversation,
// provider, or settings behavior lives here.
package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

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
	// etags caches each asset's strong validator. The files are embedded, so
	// a name's bytes never change for the life of the process: one hash per
	// file turns a reload into a 304 instead of a re-download, which is what
	// makes the no-cache contract on the scripts affordable.
	var etags sync.Map
	serve := func(c *gin.Context) {
		headers(c)
		name := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if name == "favicon.svg" {
			// The icon is the one asset with no cross-file contract: nothing
			// pairs it with a script, so a browser may keep it for hours and
			// revalidate it by ETag after that. Every other asset revalidates
			// on each load, so a rebuilt binary shows up at once.
			c.Header("Cache-Control", "public, max-age="+strconv.Itoa(iconMaxAge))
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
		if tag, ok := assetETag(assets, &etags, name); ok {
			c.Header("ETag", tag)
			if ifNoneMatch(c.GetHeader("If-None-Match"), tag) {
				c.Status(http.StatusNotModified)
				return
			}
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

// iconMaxAge is how long a browser may keep the app icon without asking: a
// few hours, so a new icon reaches an open tab the same day.
const iconMaxAge = 4 * 60 * 60

// assetETag answers name's strong ETag, hashing its bytes once and keeping
// the result for the life of the process.
func assetETag(assets fs.FS, etags *sync.Map, name string) (string, bool) {
	if tag, ok := etags.Load(name); ok {
		return tag.(string), true
	}
	data, err := fs.ReadFile(assets, name)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	tag := `"` + hex.EncodeToString(sum[:16]) + `"`
	etags.Store(name, tag)
	return tag, true
}

// ifNoneMatch answers whether an If-None-Match header names tag: a list, the
// wildcard, or the weak form a browser may echo back.
func ifNoneMatch(header, tag string) bool {
	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || part == tag || part == "W/"+tag {
			return true
		}
	}
	return false
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
	for part := range strings.SplitSeq(accept, ",") {
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
