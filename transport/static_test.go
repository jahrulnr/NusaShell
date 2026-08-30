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
	for _, needle := range []string{"data-view=\"agent\"", "data-view=\"skills\"", "data-view=\"plugins\"", "data-view=\"providers\"", "data-view=\"logs\"", "data-view=\"settings\""} {
		if !strings.Contains(html, needle) {
			t.Fatalf("index.html missing %s", needle)
		}
	}
}

func TestFrontendAssets(t *testing.T) {
	h := newHarness(t, nil)

	checks := map[string][]string{
		"/js/app.js":                {"text/javascript", "application/javascript"},
		"/styles/global.css":        {"text/css"},
		"/nusashell-mark.png":       {"image/png"},
		"/agent-offline-mascot.png": {"image/png"},
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
		contentType := resp.Header.Get("Content-Type")
		matchesContentType := false
		for _, acceptedType := range wantCT {
			matchesContentType = matchesContentType || strings.HasPrefix(contentType, acceptedType)
		}
		if !matchesContentType {
			t.Fatalf("GET %s content type = %q, want one of %q", path, contentType, wantCT)
		}
	}

	// module imports must resolve (the app is served unbundled)
	app, _ := http.Get(h.server.URL + "/js/app.js")
	appBody, _ := io.ReadAll(app.Body)
	app.Body.Close()
	for _, mod := range []string{
		"./rpc.js", "./views/agent.js", "./views/agent/render.js", "./views/agent/composer.js", "./views/agent/model-picker.js",
		"./views/skills.js", "./views/plugins.js", "./views/plugins-model.js", "./views/providers.js", "./views/logs.js", "./views/settings.js", "./ui.js", "./markdown.js",
	} {
		ref := strings.TrimPrefix(mod, "./")
		resp, err := http.Get(h.server.URL + "/js/" + ref)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("module %s not served (status %v, err %v)", mod, resp.StatusCode, err)
		}
		resp.Body.Close()
	}
	if !strings.Contains(string(appBody), "boot") {
		t.Fatalf("app.js content unexpected")
	}

	// fonts
	resp, err := http.Get(h.server.URL + "/fonts/fonts.css")
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

// TestPWAServing verifies the PWA surface: the web app manifest is served
// with its proper media type (browsers reject manifests served as
// application/octet-stream), the service worker is reachable at the root
// scope with a JavaScript MIME type, icons resolve, and index.html wires
// everything together.
func TestPWAServing(t *testing.T) {
	h := newHarness(t, nil)

	resp, err := http.Get(h.server.URL + "/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Fatalf("manifest content type = %q, want application/manifest+json", ct)
	}
	manifestBody, _ := io.ReadAll(resp.Body)
	for _, needle := range []string{`"display": "standalone"`, "icons/icon-192.png", "icons/icon-maskable-512.png"} {
		if !strings.Contains(string(manifestBody), needle) {
			t.Fatalf("manifest missing %s", needle)
		}
	}

	resp, err = http.Get(h.server.URL + "/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sw.js = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/javascript") && !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("sw.js content type = %q", ct)
	}
	swBody, _ := io.ReadAll(resp.Body)
	for _, needle := range []string{"nusashell-shell-v1", "'fetch'", "/rpc/", "/plugins/", "./js/pip.js"} {
		if !strings.Contains(string(swBody), needle) {
			t.Fatalf("sw.js missing %s", needle)
		}
	}

	for _, icon := range []string{"/icons/icon-192.png", "/icons/icon-512.png", "/icons/icon-maskable-192.png", "/icons/icon-maskable-512.png"} {
		resp, err := http.Get(h.server.URL + icon)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", icon, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
			t.Fatalf("GET %s content type = %q", icon, got)
		}
		if len(body) < 1000 {
			t.Fatalf("GET %s body too small: %d bytes", icon, len(body))
		}
	}

	resp, err = http.Get(h.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	html, _ := io.ReadAll(resp.Body)
	for _, needle := range []string{`rel="manifest"`, `"theme-color"`, "js/pwa.js", "offline-screen"} {
		if !strings.Contains(string(html), needle) {
			t.Fatalf("index.html missing %s", needle)
		}
	}
}

// TestSoundsEndpoint verifies that the embedded notification sounds are
// served at /sounds/ with the correct MIME type so the frontend can play
// them on turn-complete / turn-error events.
func TestSoundsEndpoint(t *testing.T) {
	h := newHarness(t, nil)
	for _, name := range []string{"notification.wav", "notification-error.wav"} {
		resp, err := http.Get(h.server.URL + "/sounds/" + name)
		if err != nil {
			t.Fatalf("GET /sounds/%s: %v", name, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /sounds/%s = %d, want 200", name, resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "audio/wav") && !strings.HasPrefix(ct, "audio/x-wav") && !strings.HasPrefix(ct, "application/octet-stream") {
			t.Fatalf("GET /sounds/%s content-type = %q, want audio/wav or similar", name, ct)
		}
		body, _ := io.ReadAll(resp.Body)
		if len(body) < 1000 {
			t.Fatalf("GET /sounds/%s body too small: %d bytes", name, len(body))
		}
	}
}
