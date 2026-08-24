package sttinstall

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fakeServers serves a deterministic whisper-cli archive (correct for the
// running test platform) plus a downloadable "ggml-tiny" model file, so
// Install can be exercised end to end without any network access.
type fakeServers struct {
	releaseBase string
	modelsBase  string
}

const fakeModelSize = 2048

func tarGzWhisperCli() []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("#!/bin/sh\necho whisper-cli-fake\n")
	_ = tw.WriteHeader(&tar.Header{Name: "whisper-cli", Mode: 0o755, Size: int64(len(content))})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

func zipWhisperCli() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("whisper-cli.exe")
	_, _ = f.Write([]byte("MZ-stub"))
	_ = zw.Close()
	return buf.Bytes()
}

func newFakeServers(t *testing.T) *fakeServers {
	t.Helper()
	asset, ok := engineAsset(runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Fatalf("no engine asset for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	archiveName := asset.Name
	enginePayload := tarGzWhisperCli()
	if asset.Kind == "zip" {
		enginePayload = zipWhisperCli()
	}

	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, archiveName) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(enginePayload)))
		_, _ = w.Write(enginePayload)
	}))
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "ggml-tiny.bin") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(fakeModelSize))
		_, _ = w.Write(bytes.Repeat([]byte("GGML"), fakeModelSize/4))
	}))
	t.Cleanup(release.Close)
	t.Cleanup(models.Close)
	return &fakeServers{releaseBase: release.URL, modelsBase: models.URL}
}

// pointTiny repoints the "ggml-tiny" catalog entry at the fake model payload
// for the duration of one test. Tests run sequentially in this package, so a
// straight mutation (documented here) keeps the installer API honest while
// the test stays dependency-free; each test restores the entry it touched.
func pointTiny(t *testing.T, sha string) {
	t.Helper()
	for i := range Models {
		if Models[i].ID == "ggml-tiny" {
			orig := Models[i]
			Models[i].Size = fakeModelSize
			Models[i].SHA256 = sha
			t.Cleanup(func() { Models[i] = orig })
			return
		}
	}
	t.Fatalf("ggml-tiny missing from catalog")
}

func TestInstallStagesEngineAndModel(t *testing.T) {
	if _, ok := engineAsset(runtime.GOOS, runtime.GOARCH); !ok {
		t.Skipf("whisper engine not available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	t.Setenv("WHISPER_BIN", "")
	t.Setenv("PATH", t.TempDir())
	fs := newFakeServers(t)
	dataDir := t.TempDir()
	in := New(dataDir, fs.releaseBase, fs.modelsBase)
	pointTiny(t, "")

	var phases []string
	err := in.Install(context.Background(), "ggml-tiny", func(p Progress) { phases = append(phases, p.Phase) })
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	seen := map[string]bool{}
	for _, ph := range phases {
		seen[ph] = true
	}
	for _, want := range []string{PhaseBinary, PhaseModel, PhaseVerify} {
		if !seen[want] {
			t.Errorf("phase %q never reported (got %v)", want, phases)
		}
	}
	if _, err := os.Stat(in.engineBinary()); err != nil {
		t.Errorf("engine missing after install: %v", err)
	}
	if _, err := os.Stat(in.modelPath("ggml-tiny")); err != nil {
		t.Errorf("model missing after install: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(in.modelsDir(), "*.download")); len(matches) != 0 {
		t.Errorf("leftover .download files: %v", matches)
	}
	st := in.Status()
	if !st.EngineInstalled || !st.Ready {
		t.Errorf("post-install status = %+v", st)
	}
	if !st.Supported {
		t.Error("running platform must be supported")
	}
	found := false
	for _, m := range st.Models {
		if m.ID == "ggml-tiny" && m.Installed {
			found = true
		}
	}
	if !found {
		t.Error("status must list ggml-tiny as installed")
	}
}

func TestInstallRefusesUnknownModel(t *testing.T) {
	dataDir := t.TempDir()
	in := New(dataDir, "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err := in.Install(context.Background(), "ggml-does-not-exist", nil); err == nil {
		t.Fatal("unknown model must fail")
	}
}

func TestInstallVerifiesSHA256(t *testing.T) {
	if _, ok := engineAsset(runtime.GOOS, runtime.GOARCH); !ok {
		t.Skipf("whisper engine not available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	t.Setenv("WHISPER_BIN", "")
	fs := newFakeServers(t)
	dataDir := t.TempDir()
	in := New(dataDir, fs.releaseBase, fs.modelsBase)
	pointTiny(t, strings.Repeat("0", 64)) // wrong digest

	err := in.Install(context.Background(), "ggml-tiny", nil)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch, got %v", err)
	}
	// The corrupt download must not be left behind as a usable model.
	if _, statErr := os.Stat(in.modelPath("ggml-tiny")); statErr == nil {
		t.Error("failed install must not leave a model behind")
	}
	if matches, _ := filepath.Glob(filepath.Join(in.modelsDir(), "*.download")); len(matches) != 0 {
		t.Errorf("failed install must clean .download files: %v", matches)
	}
}

func TestInstallCancelStops(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	blocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	t.Cleanup(blocker.Close)

	dataDir := t.TempDir()
	in := New(dataDir, blocker.URL, blocker.URL)
	pointTiny(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := in.Install(ctx, "ggml-tiny", nil); err == nil {
		t.Fatal("cancelled install must fail")
	}
}

func TestStatusWithoutEngine(t *testing.T) {
	t.Setenv("WHISPER_BIN", "")
	dataDir := t.TempDir()
	in := New(dataDir, "", "")
	st := in.Status()
	if len(st.Models) != len(Models) {
		t.Errorf("models listed = %d, want %d", len(st.Models), len(Models))
	}
	if st.Ready {
		t.Error("nothing installed can never be ready")
	}
	for _, m := range st.Models {
		if m.Installed {
			t.Errorf("model %q cannot be installed in an empty data dir", m.ID)
		}
	}
	// nextAction must point at the engine first (path/eng workflow order).
	if !st.EngineInstalled {
		if _, managedOK := engineAsset(runtime.GOOS, runtime.GOARCH); managedOK {
			if st.Reason != "engine" {
				t.Errorf("reason with engine missing = %q, want engine", st.Reason)
			}
		}
	}
}

func TestProgressByteCounterMonotonic(t *testing.T) {
	if _, ok := engineAsset(runtime.GOOS, runtime.GOARCH); !ok {
		t.Skipf("whisper engine not available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	t.Setenv("WHISPER_BIN", "")
	fs := newFakeServers(t)
	dataDir := t.TempDir()
	in := New(dataDir, fs.releaseBase, fs.modelsBase)
	pointTiny(t, "")

	var last, final int64
	err := in.Install(context.Background(), "ggml-tiny", func(p Progress) {
		if p.Phase != PhaseModel {
			return
		}
		if p.BytesTotal > 0 && p.BytesTotal != fakeModelSize {
			t.Errorf("model BytesTotal = %d, want %d", p.BytesTotal, fakeModelSize)
		}
		if p.BytesFetched < last {
			t.Errorf("model byte counter went backwards")
		}
		last = p.BytesFetched
		final = p.BytesFetched
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if final != fakeModelSize {
		t.Errorf("final model byte count = %d, want %d", final, fakeModelSize)
	}
}
