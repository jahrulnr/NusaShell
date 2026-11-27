package webui_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	httpdelivery "github.com/jahrulnr/nusashell/infrastructure/delivery/http"
)

const authority = "127.0.0.1:8080"

func assets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                 {Data: []byte("<nusa-app></nusa-app>")},
		"app.js":                     {Data: []byte("import './x.js';")},
		"favicon.svg":                {Data: []byte("<svg/>")},
		"styles/theme.css":           {Data: []byte("body{}")},
		"components/x.js":            {Data: []byte("export {};")},
		"vendor/pele/pele.global.js": {Data: []byte("globalThis.pele={};")},
		"vendor/pele/LICENSE":        {Data: []byte("MIT")},
	}
}

func router(t *testing.T) *gin.Engine {
	t.Helper()
	fsys := assets()
	r := httpdelivery.NewHandler(httpdelivery.Deps{Authorities: []string{authority}, WebAssets: fsys})
	return r
}

func request(r *gin.Engine, method, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestIndexServesWithSecurityHeaders(t *testing.T) {
	w := request(router(t), "GET", "/", authority)
	if w.Code != 200 {
		t.Fatalf("GET / = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type %q", ct)
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("CSP must not be set, got %q", csp)
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Error("no-cache missing")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("referrer-policy missing")
	}
}

func TestWidgetAssetsServeAndHead(t *testing.T) {
	r := router(t)
	for _, p := range []string{"/app.js", "/favicon.svg", "/styles/theme.css", "/components/x.js", "/vendor/pele/pele.global.js", "/vendor/pele/LICENSE"} {
		w := request(r, "GET", p, authority)
		if w.Code != 200 {
			t.Errorf("GET %s = %d", p, w.Code)
		}
		w = request(r, "HEAD", p, authority)
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Errorf("HEAD %s = %d len=%d", p, w.Code, w.Body.Len())
		}
	}
	if ct := request(r, "GET", "/components/x.js", authority).Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("js content-type %q", ct)
	}
	if ct := request(r, "GET", "/vendor/pele/pele.global.js", authority).Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("vendored bundle content-type %q", ct)
	}
}

// Every script and stylesheet shares one no-cache contract: a long-lived
// stylesheet served next to a fresh script pairs new markup with stale
// rules. Each still carries an ETag, so the revalidation costs a 304.
func TestAssetsNeverPairFreshJSWithStaleCSS(t *testing.T) {
	r := router(t)
	for _, p := range []string{"/styles/theme.css", "/components/x.js"} {
		w := request(r, "GET", p, authority)
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s cache-control = %q", p, got)
		}
		if w.Header().Get("ETag") == "" {
			t.Errorf("GET %s has no ETag", p)
		}
	}
}

// The icon is the one asset with no cross-file contract, so a browser may
// keep it for hours — and revalidate it by ETag when that runs out.
func TestIconIsCacheableAndRevalidates(t *testing.T) {
	r := router(t)
	w := request(r, "GET", "/favicon.svg", authority)
	if w.Code != 200 {
		t.Fatalf("GET /favicon.svg = %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=14400" {
		t.Errorf("icon cache-control = %q", got)
	}
	tag := w.Header().Get("ETag")
	if tag == "" {
		t.Fatal("icon has no ETag")
	}
	req := httptest.NewRequest("GET", "/favicon.svg", nil)
	req.Host = authority
	req.Header.Set("If-None-Match", tag)
	again := httptest.NewRecorder()
	r.ServeHTTP(again, req)
	if again.Code != 304 || again.Body.Len() != 0 {
		t.Errorf("revalidation = %d len=%d, want 304 with no body", again.Code, again.Body.Len())
	}
	// A validator that does not match still gets the bytes.
	req.Header.Set("If-None-Match", `"stale"`)
	fresh := httptest.NewRecorder()
	r.ServeHTTP(fresh, req)
	if fresh.Code != 200 || fresh.Body.Len() == 0 {
		t.Errorf("mismatched validator = %d len=%d, want 200 with the body", fresh.Code, fresh.Body.Len())
	}
}

// A directory served while the process runs is not an embedded FS: the
// hash of a file is only good for the bytes that were hashed. Changing
// those bytes must not keep answering 304 for the previous ETag, and a
// file that was not edited must still revalidate.
func TestChangedDirectoryFileDoesNotReuseItsETag(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "styles"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "app.js")
	style := filepath.Join(dir, "styles", "theme.css")
	if err := os.WriteFile(script, []byte("var v = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(style, []byte("body{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := httpdelivery.NewHandler(httpdelivery.Deps{Authorities: []string{authority}, WebAssets: os.DirFS(dir)})

	first := request(r, "GET", "/app.js", authority)
	if first.Code != 200 || first.Body.String() != "var v = 1;\n" {
		t.Fatalf("first GET = %d %q", first.Code, first.Body.String())
	}
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("app.js has no ETag")
	}
	again := getIfNoneMatch(r, "/app.js", tag)
	if again.Code != 304 || again.Body.Len() != 0 {
		t.Fatalf("unchanged app.js = %d len=%d, want 304", again.Code, again.Body.Len())
	}

	const next = "var v = 2;\n// edited while the process runs\n"
	if err := os.WriteFile(script, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := getIfNoneMatch(r, "/app.js", tag)
	if stale.Code != 200 || stale.Body.String() != next {
		t.Fatalf("edited app.js with the old ETag = %d %q, want 200 and the new bytes", stale.Code, stale.Body.String())
	}
	fresh := stale.Header().Get("ETag")
	if fresh == "" || fresh == tag {
		t.Fatalf("edited app.js ETag = %q, want a new validator", fresh)
	}
	matched := getIfNoneMatch(r, "/app.js", fresh)
	if matched.Code != 304 || matched.Body.Len() != 0 {
		t.Fatalf("the new ETag = %d len=%d, want 304", matched.Code, matched.Body.Len())
	}

	css := request(r, "GET", "/styles/theme.css", authority)
	cssTag := css.Header().Get("ETag")
	if css.Code != 200 || css.Body.String() != "body{}\n" || cssTag == "" {
		t.Fatalf("unedited css = %d %q etag %q", css.Code, css.Body.String(), cssTag)
	}
	cssAgain := getIfNoneMatch(r, "/styles/theme.css", cssTag)
	if cssAgain.Code != 304 || cssAgain.Body.Len() != 0 {
		t.Fatalf("unedited css revalidation = %d len=%d, want 304", cssAgain.Code, cssAgain.Body.Len())
	}
}

func getIfNoneMatch(r *gin.Engine, target, tag string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	req.Host = authority
	req.Header.Set("If-None-Match", tag)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestVendorRespectsGuardAndMissingIs404(t *testing.T) {
	r := router(t)
	if w := request(r, "GET", "/vendor/pele/pele.global.js", "evil.example"); w.Code != 403 {
		t.Errorf("GET vendor bundle with foreign Host = %d, want 403", w.Code)
	}
	w := request(r, "GET", "/vendor/missing.js", authority)
	if w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "problem+json") {
		t.Errorf("missing vendor asset = %d %q, want 404 problem+json", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestAssetRespectsHostGuard(t *testing.T) {
	w := request(router(t), "GET", "/", "evil.example")
	if w.Code != 403 {
		t.Fatalf("GET / with foreign Host = %d", w.Code)
	}
}

func TestUnknownPathsAreProblem404(t *testing.T) {
	r := router(t)
	for _, p := range []string{"/api/unknown", "/nope", "/components/missing.js"} {
		w := request(r, "GET", p, authority)
		if w.Code != 404 {
			t.Errorf("GET %s = %d", p, w.Code)
			continue
		}
		if !strings.Contains(w.Header().Get("Content-Type"), "problem+json") {
			t.Errorf("GET %s content-type %q — not an SPA fallback, problem expected", p, w.Header().Get("Content-Type"))
		}
	}
}

func TestNilAssetsRegistersNothing(t *testing.T) {
	r := httpdelivery.NewHandler(httpdelivery.Deps{Authorities: []string{authority}})
	w := request(r, "GET", "/", authority)
	if w.Code != 404 {
		t.Fatalf("GET / without assets = %d", w.Code)
	}
}

// navigate issues the request a browser makes when the user opens an
// address: a GET that names text/html in Accept.
func navigate(r *gin.Engine, method, target, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A path the app does not own still has to reach the app: a browser
// navigation gets the shell, so the not-found view renders instead of a
// problem+json body the browser can only show as a dead page.
func TestBrowserNavigationToUnknownPathServesTheShell(t *testing.T) {
	w := navigate(router(t), "GET", "/c/c_123", authority)
	if w.Code != 404 {
		t.Fatalf("GET /c/c_123 = %d, want 404 with the shell", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "<nusa-app>") {
		t.Fatalf("body %q, want the index shell", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Error("no-cache missing")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
}

func TestShellNavigationRespectsHostGuardAndMethod(t *testing.T) {
	r := router(t)
	if w := navigate(r, "GET", "/c/c_123", "evil.example"); w.Code != 403 {
		t.Errorf("navigation with a foreign Host = %d, want 403", w.Code)
	}
	w := navigate(r, "HEAD", "/nope", authority)
	if w.Code != 404 || w.Body.Len() != 0 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("HEAD /nope = %d len=%d type=%q", w.Code, w.Body.Len(), w.Header().Get("Content-Type"))
	}
	req := httptest.NewRequest("POST", "/nope", nil)
	req.Host = authority
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Origin", "http://"+authority)
	post := httptest.NewRecorder()
	r.ServeHTTP(post, req)
	if post.Code != 404 || !strings.Contains(post.Header().Get("Content-Type"), "problem+json") {
		t.Errorf("POST /nope = %d %q, want problem+json", post.Code, post.Header().Get("Content-Type"))
	}
}

// The API contract and machine callers keep problem+json: only a page
// load gets a page.
func TestShellNeverCoversAPIOrNonHTMLRequests(t *testing.T) {
	r := router(t)
	if w := navigate(r, "GET", "/api/nope", authority); w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "problem+json") || problemCode(t, w) != "route_not_mounted" {
		t.Errorf("GET /api/nope as a navigation = %d %q, want problem+json route_not_mounted", w.Code, w.Header().Get("Content-Type"))
	}
	req := httptest.NewRequest("GET", "/nope", nil)
	req.Host = authority
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "problem+json") || problemCode(t, w) != "not_found" {
		t.Errorf("GET /nope for JSON = %d %q, want problem+json not_found", w.Code, w.Header().Get("Content-Type"))
	}
}

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response is not JSON: %v — %s", err, w.Body)
	}
	code, _ := doc["code"].(string)
	return code
}
