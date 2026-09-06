package pet

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHTTP returns canned responses keyed by URL. Anything not registered
// 404s so an installer bug surfaces as a clear "unexpected URL" failure.
type fakeHTTP struct {
	mu       sync.Mutex
	handlers map[string]func(*http.Request) (*http.Response, error)
	got      []string
}

func newFakeHTTP() *fakeHTTP {
	return &fakeHTTP{handlers: map[string]func(*http.Request) (*http.Response, error){}}
}

func (f *fakeHTTP) Register(url string, body []byte, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[url] = func(_ *http.Request) (*http.Response, error) {
		return canned(status, body), nil
	}
}

func (f *fakeHTTP) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.got = append(f.got, req.URL.String())
	handler, ok := f.handlers[req.URL.String()]
	f.mu.Unlock()
	if !ok {
		return canned(http.StatusNotFound, []byte("not found: "+req.URL.String())), nil
	}
	return handler(req)
}

func canned(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// makeReleasePayload builds a tar.gz that mirrors the pets release layout:
// VERSION, nusashell-pets (executable bit baked into mode), and
// assets/pets/config.json.
func makeReleasePayload(t *testing.T) (data []byte, sha string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, body []byte, mode int64) {
		hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("VERSION", []byte("0.2.0"), 0o644)
	add("nusashell-pets", []byte("#!/bin/sh\necho pets\n"), 0o755)
	add("assets/pets/config.json", []byte(`{"name":"test"}`), 0o644)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	data = buf.Bytes()
	sum := sha256.Sum256(data)
	sha = hex.EncodeToString(sum[:])
	return
}

func writeExec(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
}

func testResolver(home, procRoot string) *Resolver {
	env := func(k string) string { return "" }
	return &Resolver{
		Home:     home,
		Env:      env,
		Stat:     statExists,
		ProcRoot: procRoot,
	}
}

// ---- resolver probe tests ----

func TestPetsBinaryResolutionOrder(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pets binary resolution is Linux-only")
	}
	home := t.TempDir()
	r := testResolver(home, "")

	t.Run("env root override", func(t *testing.T) {
		override := filepath.Join(home, "custom-root")
		bin := filepath.Join(override, "current", "nusashell-pets")
		writeExec(t, bin, []byte("#!/bin/sh\n"))
		r.Env = func(k string) string {
			if k == "NUSASHELL_PETS_INSTALL_ROOT" {
				return override
			}
			return ""
		}
		got, ok := r.PetsBinary()
		if !ok || got != bin {
			t.Fatalf("env override: got %q ok=%v, want %q", got, ok, bin)
		}
	})

	t.Run("default install root", func(t *testing.T) {
		r.Env = func(string) string { return "" }
		bin := filepath.Join(home, ".local/share/nusashell-pets/current/nusashell-pets")
		writeExec(t, bin, []byte("#!/bin/sh\n"))
		got, ok := r.PetsBinary()
		if !ok || got != bin {
			t.Fatalf("install root: got %q ok=%v, want %q", got, ok, bin)
		}
	})

	t.Run("launcher fallback", func(t *testing.T) {
		_ = os.RemoveAll(filepath.Join(home, ".local/share/nusashell-pets"))
		launcher := filepath.Join(home, ".local/bin/nusashell-pets")
		writeExec(t, launcher, []byte("#!/bin/sh\n"))
		got, ok := r.PetsBinary()
		if !ok || got != launcher {
			t.Fatalf("launcher: got %q ok=%v, want %q", got, ok, launcher)
		}
	})

	t.Run("not installed", func(t *testing.T) {
		clean := t.TempDir()
		rr := testResolver(clean, "")
		if _, ok := rr.PetsBinary(); ok {
			t.Fatal("PetsBinary must not resolve when nothing is installed")
		}
	})
}

func TestPetsRunningScansProcRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pets running probe is Linux-only")
	}
	root := t.TempDir()
	writeCmdline := func(pid, cmdline string) {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCmdline("42", "/home/u/.local/share/nusashell-pets/current/nusashell-pets\x00--assets\x00/path")
	writeCmdline("120", "/usr/bin/nusashell-pets-install")
	writeCmdline("9", "/usr/bin/nusashell-go")
	if !testResolver("", root).PetsRunning() {
		t.Fatal("nusashell-pets process must be detected")
	}

	clean := t.TempDir()
	if testResolver("", clean).PetsRunning() {
		t.Fatal("empty proc root must report not running")
	}

	solo := t.TempDir()
	dir := filepath.Join(solo, "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/x/nusashell-pets-install"), 0o644); err != nil {
		t.Fatal(err)
	}
	if testResolver("", solo).PetsRunning() {
		t.Fatal("install-only proc tree must report not running")
	}
}

func TestPetsInstalledVersion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pets version probe is Linux-only")
	}
	home := t.TempDir()
	root := filepath.Join(home, ".local/share/nusashell-pets/current")
	writeExec(t, filepath.Join(root, "nusashell-pets"), []byte("#!/bin/sh\n"))
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := testResolver(home, "")
	if got := r.PetsInstalledVersion(); got != "1.2.3" {
		t.Errorf("expected 1.2.3, got %q", got)
	}
}

// ---- installer end-to-end ----

func TestInstallHappyPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet install pipeline is Linux-only")
	}
	home := t.TempDir()
	payload, sha := makeReleasePayload(t)
	fake := newFakeHTTP()

	indexBody := []byte(`{"pets":{"version":"0.2.0","tag":"pets-v0.2.0","manifest":"pets-latest.json"}}`)
	fake.Register("https://example.test/release-versions.json", indexBody, http.StatusOK)
	manifestBody := []byte(fmt.Sprintf(`{"product":"pets","version":"0.2.0","files":{"linux-x64":{"name":"pets-0.2.0.tar.gz","sha256":"%s"}}}`, sha))
	fake.Register("https://example.test/download/pets-v0.2.0/pets-latest.json", manifestBody, http.StatusOK)
	fake.Register("https://example.test/download/pets-v0.2.0/pets-0.2.0.tar.gz", payload, http.StatusOK)

	in := NewWithOverrides(home, "https://example.test", "https://example.test/release-versions.json", fake)
	in.resolver = testResolver(home, "")

	var phases []string
	err := in.install(context.Background(), "", func(p Progress) { phases = append(phases, p.Phase) })
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	for _, want := range []string{PhaseResolve, PhaseDownload, PhaseVerify, PhaseExtract, PhaseActivate, PhaseLauncher} {
		if !contains(phases, want) {
			t.Errorf("missing phase %q in %v", want, phases)
		}
	}

	bin := filepath.Join(home, ".local/share/nusashell-pets/current/nusashell-pets")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("binary missing after install: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("installed binary must be executable")
	}
	launcher := filepath.Join(home, ".local/bin/nusashell-pets")
	if info, err := os.Stat(launcher); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("launcher missing or non-executable: %v", err)
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet install pipeline is Linux-only")
	}
	home := t.TempDir()
	payload, _ := makeReleasePayload(t)
	fake := newFakeHTTP()
	fake.Register("https://example.test/release-versions.json", []byte(`{"pets":{"version":"0.2.0","tag":"pets-v0.2.0","manifest":"pets-latest.json"}}`), http.StatusOK)
	fake.Register("https://example.test/download/pets-v0.2.0/pets-latest.json", []byte(`{"product":"pets","version":"0.2.0","files":{"linux-x64":{"name":"pets-0.2.0.tar.gz","sha256":"deadbeef"}}}`), http.StatusOK)
	fake.Register("https://example.test/download/pets-v0.2.0/pets-0.2.0.tar.gz", payload, http.StatusOK)

	in := NewWithOverrides(home, "https://example.test", "https://example.test/release-versions.json", fake)
	in.resolver = testResolver(home, "")
	if err := in.install(context.Background(), "", func(Progress) {}); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected SHA-256 mismatch, got %v", err)
	}
}

func TestInstallRejectsUnsafeArchiveEntry(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet install pipeline is Linux-only")
	}
	home := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "../escaped.txt", Mode: 0o644, Size: 2}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("xx")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	payload := buf.Bytes()
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	fake := newFakeHTTP()
	fake.Register("https://example.test/release-versions.json", []byte(`{"pets":{"version":"0.2.0","tag":"pets-v0.2.0","manifest":"pets-latest.json"}}`), http.StatusOK)
	fake.Register("https://example.test/download/pets-v0.2.0/pets-latest.json", []byte(fmt.Sprintf(`{"product":"pets","version":"0.2.0","files":{"linux-x64":{"name":"pets-0.2.0.tar.gz","sha256":"%s"}}}`, sha)), http.StatusOK)
	fake.Register("https://example.test/download/pets-v0.2.0/pets-0.2.0.tar.gz", payload, http.StatusOK)

	in := NewWithOverrides(home, "https://example.test", "https://example.test/release-versions.json", fake)
	in.resolver = testResolver(home, "")
	err := in.install(context.Background(), "", func(Progress) {})
	if err == nil {
		t.Fatal("expected unsafe-entry error, got nil")
	}
	if !strings.Contains(err.Error(), "unsafe archive entry") && !strings.Contains(err.Error(), "archive escapes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstallSkipsRedownloadWhenVersionReady(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet install pipeline is Linux-only")
	}
	home := t.TempDir()
	root := filepath.Join(home, ".local/share/nusashell-pets/versions/0.2.0")
	writeExec(t, filepath.Join(root, "nusashell-pets"), []byte("#!/bin/sh\n"))
	if err := os.MkdirAll(filepath.Join(root, "assets/pets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets/pets/config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := newFakeHTTP()
	in := NewWithOverrides(home, "https://example.test", "https://example.test/release-versions.json", fake)
	in.resolver = testResolver(home, "")

	var phases []string
	err := in.install(context.Background(), "0.2.0", func(p Progress) { phases = append(phases, p.Phase) })
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	for _, p := range phases {
		if p == PhaseDownload || p == PhaseExtract {
			t.Errorf("expected no download/extract phases on cached version, got %v", phases)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestInstallAcceptsRealManifestShape pins the exact release-manifest.mjs
// output: top-level product + version are strings, payloads live under
// "files". A flat decode of the whole doc (the earlier bug) fails on the
// string-typed version key with "cannot unmarshal string …". This test
// reproduces the user-facing failure so it stays fixed.
func TestInstallAcceptsRealManifestShape(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet install pipeline is Linux-only")
	}
	home := t.TempDir()
	payload, sha := makeReleasePayload(t)
	fake := newFakeHTTP()
	// Identical bytes to release-manifest.mjs output for pets.
	manifestBody := fmt.Sprintf(
		`{"product":"pets","version":"0.1.3","files":{"linux-x64":{"name":"nusashell-pets-0.1.3-linux-x64.tar.gz","sha256":"%s"}}}`,
		sha,
	)
	fake.Register("https://example.test/release-versions.json", []byte(`{"pets":{"version":"0.1.3","tag":"pets-v0.1.3","manifest":"pets-latest.json"}}`), http.StatusOK)
	fake.Register("https://example.test/download/pets-v0.1.3/pets-latest.json", []byte(manifestBody), http.StatusOK)
	fake.Register("https://example.test/download/pets-v0.1.3/nusashell-pets-0.1.3-linux-x64.tar.gz", payload, http.StatusOK)

	in := NewWithOverrides(home, "https://example.test", "https://example.test/release-versions.json", fake)
	in.resolver = testResolver(home, "")
	if err := in.install(context.Background(), "", func(Progress) {}); err != nil {
		t.Fatalf("install failed on release-manifest shape: %v", err)
	}
	bin := filepath.Join(home, ".local/share/nusashell-pets/current/nusashell-pets")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary missing after install: %v", err)
	}
}

// TestResolveAssetSkipsNonPayloadKeys guards the parser against any future
// top-level key that is not an object with a name — the version/product
// string keys are the exact regression that shipped.
func TestResolveAssetSkipsNonPayloadKeys(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("asset resolution is Linux-only")
	}
	fake := newFakeHTTP()
	fake.Register("https://example.test/download/pets-v9.9.9/pets-latest.json",
		[]byte(`{"product":"pets","version":"9.9.9","files":{"linux-x64":{"name":"ok.tar.gz","sha256":"aaa"}}}`),
		http.StatusOK)
	in := NewWithOverrides(t.TempDir(), "https://example.test", "https://example.test/release-versions.json", fake)
	got, err := in.resolveAsset(context.Background(), &stream{Version: "9.9.9", Tag: "pets-v9.9.9", Manifest: "pets-latest.json"})
	if err != nil {
		t.Fatalf("resolveAsset failed: %v", err)
	}
	if got.Name != "ok.tar.gz" {
		t.Errorf("expected ok.tar.gz, got %q", got.Name)
	}
}

func TestLaunchSingleFlightAndStop(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet process spawn is Linux-only")
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".local/share/nusashell-pets/current/nusashell-pets")
	writeExec(t, bin, []byte("#!/bin/sh\nsleep 60\n"))
	r := testResolver(home, "/proc")
	in := NewWithResolver(r, "", "", nil)
	t.Cleanup(func() { _ = in.Stop() })

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := in.Launch()
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("launch: %v", err)
		}
	}
	pids := r.petsPIDsMatching(bin)
	if len(pids) != 1 {
		t.Fatalf("want exactly 1 pet process after spam launch, got %d %v", len(pids), pids)
	}
	if !in.Status().Running {
		t.Fatal("status must report running after launch")
	}

	if err := in.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.petsPIDsMatching(bin)) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(r.petsPIDsMatching(bin)); n != 0 {
		t.Fatalf("want 0 pet processes after stop, got %d", n)
	}
	if in.Status().Running {
		t.Fatal("status must report not running after stop")
	}
}

func TestLaunchAfterStopSpawnsAgain(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet process spawn is Linux-only")
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".local/share/nusashell-pets/current/nusashell-pets")
	writeExec(t, bin, []byte("#!/bin/sh\nsleep 60\n"))
	r := testResolver(home, "/proc")
	in := NewWithResolver(r, "", "", nil)
	t.Cleanup(func() { _ = in.Stop() })

	if _, err := in.Launch(); err != nil {
		t.Fatal(err)
	}
	if err := in.Stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(r.petsPIDsMatching(bin)) > 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := in.Launch(); err != nil {
		t.Fatal(err)
	}
	if n := len(r.petsPIDsMatching(bin)); n != 1 {
		t.Fatalf("want 1 process after relaunch, got %d", n)
	}
}

func TestLaunchInjectsBackendEnvAndWSURLFlag(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pet process spawn is Linux-only")
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".local/share/nusashell-pets/current/nusashell-pets")
	writeExec(t, bin, []byte("#!/bin/sh\nsleep 60\n"))
	r := testResolver(home, "/proc")
	in := NewWithResolver(r, "", "", nil)
	in.SetBackend("0.0.0.0", "7777")
	t.Cleanup(func() { _ = in.Stop() })

	if _, err := in.Launch(); err != nil {
		t.Fatal(err)
	}
	pids := r.petsPIDsMatching(bin)
	if len(pids) != 1 {
		t.Fatalf("want 1 process, got %d", len(pids))
	}
	pid := pids[0]
	environ, err := os.ReadFile(filepath.Join("/proc", fmt.Sprintf("%d", pid), "environ"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(environ)
	if !strings.Contains(env, "NUSASHELL_PORT=7777") {
		t.Fatalf("child env missing NUSASHELL_PORT=7777: %q", env)
	}
	if !strings.Contains(env, "NUSASHELL_HOST=127.0.0.1") {
		t.Fatalf("wildcard bind must rewrite host to loopback, env=%q", env)
	}
	if !strings.Contains(env, "NUSASHELL_WS_URL=ws://127.0.0.1:7777/ws") {
		t.Fatalf("child env missing ws url, env=%q", env)
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", fmt.Sprintf("%d", pid), "cmdline"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.ReplaceAll(string(cmdline), "\x00", " ")
	if !strings.Contains(args, "--ws-url") || !strings.Contains(args, "ws://127.0.0.1:7777/ws") {
		t.Fatalf("child argv must pass --ws-url, got %q", args)
	}
}
