package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPlatformAsset(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "codex-x86_64-unknown-linux-musl.tar.gz"},
		{"linux", "arm64", "codex-aarch64-unknown-linux-musl.tar.gz"},
		{"darwin", "arm64", "codex-aarch64-apple-darwin.tar.gz"},
		{"darwin", "amd64", "codex-x86_64-apple-darwin.tar.gz"},
		{"windows", "amd64", "codex-x86_64-pc-windows-msvc.exe.tar.gz"},
		{"windows", "arm64", "codex-aarch64-pc-windows-msvc.exe.tar.gz"},
		{"freebsd", "amd64", ""}, // unsupported
	}
	for _, tt := range tests {
		got := PlatformAsset(tt.goos, tt.goarch)
		if got != tt.want {
			t.Errorf("PlatformAsset(%s, %s) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestPlatformAssetMatchesCurrentPlatform(t *testing.T) {
	asset := PlatformAsset(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		t.Skipf("platform %s/%s not supported by Codex releases", runtime.GOOS, runtime.GOARCH)
	}
}

func TestParseVersionTag(t *testing.T) {
	tests := []struct {
		tag, want string
	}{
		{"rust-v0.147.0", "0.147.0"},
		{"v0.148.0-alpha.19", "0.148.0-alpha.19"},
		{"rust-v0.148.0-alpha.19", "0.148.0-alpha.19"},
		{"0.147.0", "0.147.0"},
		{"", ""},
		{"garbage", "garbage"},
	}
	for _, tt := range tests {
		got := parseVersionTag(tt.tag)
		if got != tt.want {
			t.Errorf("parseVersionTag(%q) = %q, want %q", tt.tag, got, tt.want)
		}
	}
}

func TestBinaryPath(t *testing.T) {
	m := &Manager{BaseDir: "/tmp/test-runtimes"}
	got := m.BinaryPath("0.147.0")
	want := filepath.Join("/tmp/test-runtimes", "0.147.0", "codex")
	if runtime.GOOS == "windows" {
		want = filepath.Join("/tmp/test-runtimes", "0.147.0", "codex.exe")
	}
	if got != want {
		t.Errorf("BinaryPath = %q, want %q", got, want)
	}
}

func TestLoadManifestMissing(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{BaseDir: dir}
	man, err := m.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest on missing dir: %v", err)
	}
	if man.ActiveVersion != "" {
		t.Errorf("ActiveVersion = %q, want empty", man.ActiveVersion)
	}
	if man.Installed == nil {
		t.Fatal("Installed map should be initialized")
	}
}

func TestSaveAndLoadManifest(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{BaseDir: dir}

	man := &Manifest{
		ActiveVersion: "0.147.0",
		Installed: map[string]InstalledVer{
			"0.147.0": {
				Path:   filepath.Join(dir, "0.147.0", "codex"),
				Source: "github",
			},
		},
	}
	if err := m.SaveManifest(man); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	loaded, err := m.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if loaded.ActiveVersion != "0.147.0" {
		t.Errorf("ActiveVersion = %q, want 0.147.0", loaded.ActiveVersion)
	}
	v, ok := loaded.Installed["0.147.0"]
	if !ok {
		t.Fatal("version 0.147.0 not in installed map")
	}
	if v.Source != "github" {
		t.Errorf("Source = %q, want github", v.Source)
	}
}

func TestEnsureBinaryFindsExistingInstall(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{BaseDir: dir}

	// Simulate an existing install
	version := "0.147.0"
	versionDir := filepath.Join(dir, version)
	os.MkdirAll(versionDir, 0o755)
	binName := BinaryName
	if runtime.GOOS == "windows" {
		binName = BinaryNameWindows
	}
	binPath := filepath.Join(versionDir, binName)
	os.WriteFile(binPath, []byte("fake binary"), 0o755)

	man := &Manifest{
		ActiveVersion: version,
		Installed: map[string]InstalledVer{
			version: {Path: binPath, Source: "test"},
		},
	}
	m.SaveManifest(man)

	got, err := m.EnsureBinary(t.Context())
	if err != nil {
		t.Fatalf("EnsureBinary: %v", err)
	}
	if got != binPath {
		t.Errorf("EnsureBinary = %q, want %q", got, binPath)
	}
}

func TestEnsureBinaryReturnsErrorForUnsupportedPlatform(t *testing.T) {
	// This test only works if we can mock the platform, which we can't
	// easily do. Instead, test that PlatformAsset returns empty for
	// unsupported platforms (already tested above).
	// Skip on supported platforms.
	if PlatformAsset(runtime.GOOS, runtime.GOARCH) != "" {
		t.Skip("current platform is supported")
	}
	dir := t.TempDir()
	m := &Manager{BaseDir: dir}
	_, err := m.DownloadLatest(t.Context())
	if err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}
