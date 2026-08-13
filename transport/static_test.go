package transport

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFrontendServed(t *testing.T) {
	h := newHarness(t, nil)

	resp, err := http.Get(h.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type = %q", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	html := string(b)
	if !strings.Contains(html, "<title>NusaShell</title>") {
		t.Fatalf("index.html missing title")
	}
	for _, needle := range []string{"data-view=\"agent\"", "data-view=\"skills\"", "data-view=\"mcp\"", "data-view=\"providers\"", "data-view=\"logs\"", "data-view=\"settings\""} {
		if !strings.Contains(html, needle) {
			t.Fatalf("index.html missing %s", needle)
		}
	}
}

func TestFrontendAssets(t *testing.T) {
	h := newHarness(t, nil)

	checks := map[string]string{
		"/js/app.js":                  "text/javascript",
		"/styles/global.css":          "text/css",
		"/styles/electron-parity.css": "text/css",
		"/nusashell-mark.png":         "image/png",
	}
	for path, wantCT := range checks {
		resp, err := http.Get(h.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), wantCT) {
			t.Fatalf("GET %s content type = %q, want %q", path, resp.Header.Get("Content-Type"), wantCT)
		}
	}

	// module imports must resolve (the app is served unbundled)
	app, _ := http.Get(h.server.URL + "/js/app.js")
	appBody, _ := io.ReadAll(app.Body)
	app.Body.Close()
	for _, mod := range []string{"./rpc.js", "./views/agent.js", "./views/skills.js", "./views/mcp.js", "./views/providers.js", "./views/logs.js", "./views/settings.js", "./ui.js", "./markdown.js"} {
		ref := strings.TrimPrefix(mod, "./")
		resp, err := http.Get(h.server.URL + "/js/" + ref)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("module %s not served (status %v, err %v)", mod, resp.StatusCode, err)
		}
		resp.Body.Close()
	}
	resp, err := http.Get(h.server.URL + "/styles/responsive.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("responsive.css = %d", resp.StatusCode)
	}
	if !strings.Contains(string(appBody), "boot") {
		t.Fatalf("app.js content unexpected")
	}

	// fonts
	resp, err = http.Get(h.server.URL + "/fonts/fonts.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fonts.css = %d", resp.StatusCode)
	}

	// 404 handling
	resp, err = http.Get(h.server.URL + "/nope.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /nope.js = %d, want 404", resp.StatusCode)
	}
}
