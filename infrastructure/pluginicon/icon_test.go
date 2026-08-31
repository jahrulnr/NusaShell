package pluginicon

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tinyPNG = "\x89\x50\x4e\x47\x0d\x0a\x1a\x0a\x00\x00\x00\x0dIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0dIDATx\x9cc\xf8\xcf\xc0\x00\x00\x00\x03\x00\x01\x86\xa0\x5e\x27\x00\x00\x00\x00IEND\xaeB`\x82"

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLocalTextAndRemote(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"📝":                            "📝",
		"N":                            "N",
		"":                             "",
		"  ":                           "",
		"https://example.com/icon.png": "https://example.com/icon.png",
		"http://example.com/i.png":     "http://example.com/i.png",
	}
	for icon, want := range cases {
		if got := ResolveLocal(icon, dir); got != want {
			t.Errorf("ResolveLocal(%q) = %q, want %q", icon, got, want)
		}
	}
}

func TestResolveLocalFileIcons(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "icon.png", tinyPNG)
	writeFile(t, dir, "assets/icon.png", tinyPNG)

	forms := []string{"file://icon.png", "file://assets/icon.png", "./icon.png", "icon.png", "assets/icon.png"}
	for _, form := range forms {
		got := ResolveLocal(form, dir)
		if !strings.HasPrefix(got, "data:image/png;base64,") {
			t.Errorf("ResolveLocal(%q) = %q, want data URL", form, got)
		}
	}

	// Absolute file URL must use the file:/// + forward-slash form so it
	// parses as an absolute URL on every OS (on Windows, file://C:\... is
	// treated as a relative path with a drive-letter prefix).
	abs := filepath.Join(dir, "icon.png")
	got := ResolveLocal("file:///"+filepath.ToSlash(abs), dir)
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("absolute file URL = %q, want data URL", got)
	}
}

func TestResolveLocalRejectsOutsideAndInvalid(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "icon.png", tinyPNG)

	if got := ResolveLocal("file://"+filepath.Join(outside, "icon.png"), dir); got != FallbackIcon {
		t.Errorf("outside icon = %q, want fallback", got)
	}

	writeFile(t, dir, "text.txt", "not a png")
	if got := ResolveLocal("file://text.txt", dir); got != FallbackIcon {
		t.Errorf("non-png = %q, want fallback", got)
	}

	if got := ResolveLocal("file://missing.png", dir); got != FallbackIcon {
		t.Errorf("missing icon = %q, want fallback", got)
	}
}

func TestIconPath(t *testing.T) {
	cases := map[string]string{
		"📝":                      "",
		"N":                      "",
		"https://x/i.png":        "",
		"file://icon.png":        "icon.png",
		"file://assets/icon.png": "assets/icon.png",
		"file:///abs/icon.png":   "",
		"./icon.png":             "icon.png",
		"assets/icon.png":        "assets/icon.png",
		"icon.png":               "icon.png",
		"app-icon.svg":           "app-icon.svg",
	}
	for icon, want := range cases {
		if got := IconPath(icon); got != want {
			t.Errorf("IconPath(%q) = %q, want %q", icon, got, want)
		}
	}
}

func TestDataURLRoundTrip(t *testing.T) {
	data := []byte(tinyPNG)
	if !IsPNG(data) {
		t.Fatal("fixture should be a PNG")
	}
	url := DataURL(data)
	if !bytes.Equal(data, mustDecode(t, url)) {
		t.Fatal("data URL does not round-trip")
	}
	if IsPNG([]byte("nope")) {
		t.Fatal("non-PNG detected as PNG")
	}
}

func mustDecode(t *testing.T, dataURL string) []byte {
	t.Helper()
	payload := strings.TrimPrefix(dataURL, "data:image/png;base64,")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
