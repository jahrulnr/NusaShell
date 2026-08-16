package plugininstall

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/pluginfs"
)

func writeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if strings.HasSuffix(name, "/") {
			header.Typeflag = tar.TypeDir
			header.Size = 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == 0 {
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTarGz(t *testing.T) {
	stage := t.TempDir()
	data := writeTarGz(t, map[string]string{
		"manifest.json":  `{"id":"test"}`,
		"mcp/":           "",
		"mcp/server.cjs": "#!/usr/bin/env node\n",
		"ui/":            "",
		"ui/index.html":  "<html></html>",
	})
	if err := extractTarGz(io.NopCloser(bytes.NewReader(data)), stage); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "manifest.json")); err != nil {
		t.Fatalf("manifest.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "mcp", "server.cjs")); err != nil {
		t.Fatalf("mcp/server.cjs missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "ui", "index.html")); err != nil {
		t.Fatalf("ui/index.html missing: %v", err)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	stage := t.TempDir()
	data := writeTarGz(t, map[string]string{
		"../evil": "boom",
	})
	if err := extractTarGz(io.NopCloser(bytes.NewReader(data)), stage); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func writeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZip(t *testing.T) {
	stage := t.TempDir()
	data := writeZip(t, map[string]string{
		"manifest.json":  `{"id":"test"}`,
		"mcp/server.cjs": "#!/usr/bin/env node\n",
	})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := extractZip(zr, stage); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "manifest.json")); err != nil {
		t.Fatalf("manifest.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "mcp", "server.cjs")); err != nil {
		t.Fatalf("mcp/server.cjs missing: %v", err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	stage := t.TempDir()
	data := writeZip(t, map[string]string{
		"../evil": "boom",
	})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := extractZip(zr, stage); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestFindUniqueManifestDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findUniqueManifestDir(root)
	if err != nil {
		t.Fatalf("findUniqueManifestDir: %v", err)
	}
	if got != filepath.Join(root, "a") {
		t.Fatalf("dir = %q, want %q", got, filepath.Join(root, "a"))
	}

	// Two manifests → ambiguous.
	if err := os.MkdirAll(filepath.Join(root, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b", "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findUniqueManifestDir(root); err == nil {
		t.Fatal("expected ambiguous manifest error")
	}
}

func TestParseGitHubURL(t *testing.T) {
	cases := []struct {
		input       string
		owner, repo string
		ref         string
		subdir      string
		wantErr     bool
	}{
		{input: "jahrulnr/NusaShell-mcp", owner: "jahrulnr", repo: "NusaShell-mcp"},
		{input: "https://github.com/jahrulnr/NusaShell-mcp", owner: "jahrulnr", repo: "NusaShell-mcp"},
		{input: "https://github.com/jahrulnr/NusaShell-mcp.git", owner: "jahrulnr", repo: "NusaShell-mcp"},
		{input: "https://github.com/jahrulnr/NusaShell-mcp/tree/master", owner: "jahrulnr", repo: "NusaShell-mcp", ref: "master"},
		{input: "https://github.com/jahrulnr/NusaShell-mcp/tree/master/notes", owner: "jahrulnr", repo: "NusaShell-mcp", ref: "master", subdir: "notes"},
		{input: "https://github.com/jahrulnr/NusaShell-mcp/", owner: "jahrulnr", repo: "NusaShell-mcp"},
		{input: "gitlab.com/owner/repo", wantErr: true},
		{input: "https://evil.com/owner/repo", wantErr: true},
		{input: "", wantErr: true},
	}
	for _, c := range cases {
		owner, repo, ref, subdir, err := parseGitHubURL(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got none", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.input, err)
			continue
		}
		if owner != c.owner || repo != c.repo || ref != c.ref || subdir != c.subdir {
			t.Errorf("%q: got (%q, %q, %q, %q), want (%q, %q, %q, %q)",
				c.input, owner, repo, ref, subdir, c.owner, c.repo, c.ref, c.subdir)
		}
	}
}

func validManifest() string {
	return `{
		"id": "test.plugin",
		"name": "Test",
		"version": "1.0.0",
		"icon": "T",
		"mcp": {
			"transport": "stdio",
			"command": "node",
			"args": ["mcp/server.cjs"]
		}
	}`
}

func newTestInstaller(t *testing.T, store pluginStore) *Installer {
	t.Helper()
	i := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return i
}

func TestInstallFromZip(t *testing.T) {
	store, err := pluginfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := writeZip(t, map[string]string{
		"manifest.json":  validManifest(),
		"mcp/server.cjs": "#!/usr/bin/env node\nconsole.log('ok');\n",
	})

	i := newTestInstaller(t, store)
	plugin, err := i.Install(context.Background(), domain.PluginInstallRequest{
		Source: domain.InstallSourceZip,
		Data:   data,
	})
	if err != nil {
		t.Fatalf("install zip: %v", err)
	}
	if plugin.Manifest.ID != "test.plugin" {
		t.Fatalf("installed id = %q, want test.plugin", plugin.Manifest.ID)
	}
	if _, err := os.Stat(filepath.Join(plugin.InstallPath, "mcp", "server.cjs")); err != nil {
		t.Fatalf("installed server.cjs missing: %v", err)
	}
}

func TestInstallFromZipRejectsNoManifest(t *testing.T) {
	store, err := pluginfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := writeZip(t, map[string]string{"readme.txt": "hello"})

	i := newTestInstaller(t, store)
	_, err = i.Install(context.Background(), domain.PluginInstallRequest{
		Source: domain.InstallSourceZip,
		Data:   data,
	})
	if err == nil || !strings.Contains(err.Error(), "no manifest.json") {
		t.Fatalf("expected manifest error, got %v", err)
	}
}

func TestInstallRejectsUnknownSource(t *testing.T) {
	store, err := pluginfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	i := newTestInstaller(t, store)
	_, err = i.Install(context.Background(), domain.PluginInstallRequest{Source: "ftp"})
	if err == nil {
		t.Fatal("expected unsupported source error")
	}
}

func TestCatalog(t *testing.T) {
	versions := map[string]any{
		"files": map[string]any{"version": "0.1.1", "tag": "files-v0.1.1", "releasedAt": "2026-08-16T00:00:00Z"},
		"notes": map[string]any{"version": "1.0.0", "tag": "notes-v1.0.0", "releasedAt": "2026-08-16T00:00:00Z"},
		"mail":  map[string]any{"version": "0.1.0", "tag": "mail-v0.1.0", "releasedAt": "2026-08-16T00:00:00Z"},
	}
	manifests := map[string]string{
		"notes": `{"id":"nusashell.notes","name":"Notes","version":"1.0.0","icon":"📝","description":"Simple notes.","mcp":{"transport":"stdio","command":"node","args":["mcp/server.cjs"]}}`,
		"files": `{"id":"nusashell.files","name":"Files","version":"0.1.1","icon":"file://icon.png","description":"Filesystem search.","mcp":{"transport":"stdio","command":"node","args":["mcp/server.cjs"]}}`,
	}
	const tinyPNG = "\x89\x50\x4e\x47\x0d\x0a\x1a\x0a\x00\x00\x00\x0dIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\x0dIDATx\x9cc\xf8\xcf\xc0\x00\x00\x00\x03\x00\x01\x86\xa0\x5e\x27\x00\x00\x00\x00IEND\xaeB`\x82"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/versions.json":
			_ = json.NewEncoder(w).Encode(versions)
		case strings.HasPrefix(r.URL.Path, "/notes/manifest.json"):
			_, _ = w.Write([]byte(manifests["notes"]))
		case strings.HasPrefix(r.URL.Path, "/files/manifest.json"):
			_, _ = w.Write([]byte(manifests["files"]))
		case r.URL.Path == "/files/icon.png":
			_, _ = w.Write([]byte(tinyPNG))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store, err := pluginfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	i := newTestInstaller(t, store)
	i.rawBaseURL = srv.URL

	entries, err := i.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("catalog entries = %d, want 2 (mail excluded)", len(entries))
	}
	byID := map[string]domain.PluginCatalogEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	notes := byID["notes"]
	if notes.PluginID != "nusashell.notes" || notes.Version != "1.0.0" || notes.Name != "Notes" {
		t.Fatalf("notes entry wrong: %+v", notes)
	}
	if _, ok := byID["mail"]; ok {
		t.Fatal("mail must be excluded from the catalog")
	}
	files := byID["files"]
	if !strings.HasPrefix(files.Icon, "data:image/png;base64,") {
		t.Fatalf("files file:// icon must resolve to a data URL, got %q", files.Icon)
	}
}
