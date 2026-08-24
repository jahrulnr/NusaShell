package ttsinstall

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipBytes builds an in-memory zip archive with the given name->content files.
func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// fakeBinaryServer serves a deterministic binary archive per platform plus
// voice model files, so Install can be exercised without network.
func fakeBinaryServer(t *testing.T, dataDir string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	zipBin := zipBytes(t, map[string]string{
		"piper/piper":            "#!/bin/sh\necho piper-fake",
		"piper/espeak-ng-data/x": "x",
	})
	tarBin := tarGzBytes(t, map[string]string{
		"piper/piper":            "#!/bin/sh\necho piper-fake",
		"piper/espeak-ng-data/x": "x",
	})
	onnx := bytes.Repeat([]byte("ONNX"), 1000) // 4000 bytes
	json := []byte(`{"audio":{}}`)
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/bin/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/bin/")
		var payload []byte
		switch {
		case strings.HasSuffix(name, ".zip"):
			payload = zipBin
		case strings.Contains(name, ".tar.gz"):
			payload = tarBin
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/voice/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".onnx"):
			w.Header().Set("Content-Length", fmt.Sprint(len(onnx)))
			_, _ = w.Write(onnx)
		default:
			_, _ = w.Write(json)
		}
	})
	t.Cleanup(srv.Close)
	return srv
}

// tarGzBytes builds an in-memory piper-rooted tar.gz with mode 0755 files.
func tarGzBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func withFakeBin(t *testing.T, dir string) {
	t.Helper()
	old := os.Getenv("PIPER_BIN")
	t.Setenv("PIPER_BIN", "")
	_ = old
	t.Cleanup(func() { _ = os.Setenv("PIPER_BIN", old) })
}

func TestCatalogHasTwoVoices(t *testing.T) {
	if len(Voices) != 2 {
		t.Fatalf("expected 2 catalog voices, got %d", len(Voices))
	}
	var ids []string
	for _, v := range Voices {
		ids = append(ids, v.ID)
	}
	want := []string{"id_ID-news_tts-medium", "en_US-lessac-high"}
	for _, id := range want {
		found := false
		for _, got := range ids {
			if got == id {
				found = true
			}
		}
		if !found {
			t.Errorf("catalog voice %q missing (got %v)", id, ids)
		}
	}
}

func TestStatusDetectsInstalledVoice(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	inst := New(dataDir, fakeBinaryServer(t, dataDir).URL)
	st := inst.status()
	if st.BinaryInstalled {
		t.Error("binary must not be detected before install")
	}
	if st.Voices[0].Installed {
		t.Error("voice must not be installed yet")
	}
	// Touch voice files on disk.
	vd := inst.voicesDirectory()
	if err := os.MkdirAll(vd, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".onnx", ".onnx.json"} {
		if err := os.WriteFile(filepath.Join(vd, st.Voices[0].ID+suffix), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st = inst.status()
	if !st.Voices[0].Installed {
		t.Error("voice on disk must report installed")
	}
}

func TestInstallDownloadsBinaryAndVoice(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	base := fakeBinaryServer(t, dataDir).URL
	inst := New(dataDir, base)

	var phases []string
	err := inst.install(context.Background(), "id_ID-news_tts-medium", func(p Progress) {
		phases = append(phases, p.Phase)
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	seen := map[string]bool{}
	for _, ph := range phases {
		seen[ph] = true
	}
	for _, want := range []string{PhaseBinary, PhaseVoice, PhaseVerify} {
		if !seen[want] {
			t.Errorf("phase %q never reported (got %v)", want, phases)
		}
	}
	// Binary extracted at <data>/piper/<platform>/piper.
	binPath := inst.binaryPath()
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("installed binary missing at %s: %v", binPath, err)
	}
	content, _ := os.ReadFile(binPath)
	if string(content) != "#!/bin/sh\necho piper-fake" {
		t.Errorf("unexpected binary content %q", content)
	}
	// Voice files in place.
	for _, suffix := range []string{".onnx", ".onnx.json"} {
		if _, err := os.Stat(filepath.Join(inst.voicesDirectory(), "id_ID-news_tts-medium"+suffix)); err != nil {
			t.Errorf("voice file %s missing: %v", suffix, err)
		}
	}
	// Status now reports installed.
	st := inst.status()
	if !st.BinaryInstalled || !st.Voices[0].Installed {
		t.Errorf("status after install = %+v, want both installed", st)
	}
	if st.Voices[0].SizeBytes <= 0 {
		t.Errorf("voice size not surfaced, got %d", st.Voices[0].SizeBytes)
	}
}

// The piper release tarball ships versioned .so files plus unversioned
// symlinks; the extractor must recreate them or the dynamic linker fails
// with "file too short" (regression guard).
func TestInstallRecreatesSymlinks(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		for _, th := range []struct {
			name string
			body string
			link string
		}{
			{name: "piper/piper", body: "#!/bin/sh\nexit 0"},
			{name: "piper/libengine.so.1.2.3", body: "ELF"},
			{name: "piper/espeak-ng-data/dict", body: "d"},
			{name: "piper/libengine.so", link: "libengine.so.1.2.3"},
		} {
			hdr := &tar.Header{Name: th.name, Mode: 0o755}
			if th.link != "" {
				hdr.Typeflag = tar.TypeSymlink
				hdr.Linkname = th.link
			} else {
				hdr.Typeflag = tar.TypeReg
				hdr.Size = int64(len(th.body))
			}
			_ = tw.WriteHeader(hdr)
			if th.link == "" {
				_, _ = tw.Write([]byte(th.body))
			}
		}
		_ = tw.Close()
		_ = gw.Close()
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()
	inst := New(dataDir, srv.URL)
	if err := inst.install(context.Background(), "id_ID-news_tts-medium", nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	link := filepath.Join(inst.platformDir(), "libengine.so")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("symlink not recreated: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink (mode %v)", link, info.Mode())
	}
	dest, _ := os.Readlink(link)
	if dest != "libengine.so.1.2.3" {
		t.Errorf("symlink target = %q, want libengine.so.1.2.3", dest)
	}
}

func TestInstallUnknownVoiceFails(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	inst := New(dataDir, fakeBinaryServer(t, dataDir).URL)
	if err := inst.install(context.Background(), "nope", func(Progress) {}); err == nil {
		t.Fatal("unknown voice must fail")
	}
}

func TestInstallBadArchiveFailsCleanly(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not a zip file"))
	}))
	defer srv.Close()
	inst := New(dataDir, srv.URL)
	err := inst.install(context.Background(), "en_US-lessac-high", func(Progress) {})
	if err == nil {
		t.Fatal("corrupt binary payload must fail")
	}
	if _, statErr := os.Stat(inst.binaryPath()); statErr == nil {
		t.Error("failed install must not leave a binary behind")
	}
}

func TestInstallContextCancelStops(t *testing.T) {
	dataDir := t.TempDir()
	withFakeBin(t, dataDir)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // hang until released
	}))
	defer srv.Close()
	defer close(block)
	inst := New(dataDir, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := inst.install(ctx, "id_ID-news_tts-medium", func(Progress) {})
	if err == nil {
		t.Fatal("cancelled install must fail")
	}
}
