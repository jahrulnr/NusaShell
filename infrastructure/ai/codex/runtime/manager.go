// Package runtime manages the official Codex CLI binary as a NusaShell-managed
// sidecar. It handles platform detection, download from GitHub releases,
// verification, and versioned installation.
//
// The managed binary lives under ~/.nusashell/runtimes/codex/{version}/codex
// and can be used for:
//   - Server-side compaction via `codex app-server` JSON-RPC (thread/compact/start)
//   - ACP protocol integration (future)
//   - Any other Codex CLI subprocess use case
//
// Design based on .experimental/nusashell-codex-provider-technical-design.md
// §8 (Runtime Management) and §9 (Download Strategy).
package runtime

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nusashell/pkg/archive"
	"nusashell/pkg/fetch"
	"nusashell/pkg/httpclient"
)

// Default constants for runtime management.
const (
	// GitHubAPILatest is the GitHub API endpoint for the latest Codex release.
	GitHubAPILatest = "https://api.github.com/repos/openai/codex/releases/latest"

	// GitHubDownloadBase is the base URL for downloading release assets.
	GitHubDownloadBase = "https://github.com/openai/codex/releases/download"

	// RuntimeDirName is the subdirectory under ~/.nusashell/ for Codex runtimes.
	RuntimeDirName = "runtimes/codex"

	// ManifestName is the manifest file name inside the runtime directory.
	ManifestName = "manifest.json"

	// BinaryName is the executable name on non-Windows platforms.
	BinaryName = "codex"

	// BinaryNameWindows is the executable name on Windows.
	BinaryNameWindows = "codex.exe"

	// downloadTimeout is the maximum time for a single download attempt.
	downloadTimeout = 10 * time.Minute

	// httpTimeout is the timeout for HTTP metadata calls.
	httpTimeout = 30 * time.Second
)

// Manager handles Codex runtime discovery, download, and installation.
type Manager struct {
	// BaseDir is the root directory for runtimes, typically ~/.nusashell/runtimes/codex.
	BaseDir string

	// HTTPClient is used for downloads. Defaults to the central outbound
	// client when nil.
	HTTPClient *http.Client
}

// NewManager creates a Manager with BaseDir set to ~/.nusashell/runtimes/codex.
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("runtime: cannot find home dir: %w", err)
	}
	return &Manager{
		BaseDir:    filepath.Join(home, ".nusashell", RuntimeDirName),
		HTTPClient: httpclient.NewWithTimeout(downloadTimeout),
	}, nil
}

// Manifest tracks installed Codex runtime versions.
type Manifest struct {
	ActiveVersion string                  `json:"activeVersion"`
	Installed     map[string]InstalledVer `json:"installed"`
}

// InstalledVer records metadata about one installed runtime version.
type InstalledVer struct {
	Path        string    `json:"path"`
	Source      string    `json:"source"`
	InstalledAt time.Time `json:"installedAt"`
	SHA256      string    `json:"sha256,omitempty"`
}

// githubRelease is the subset of GitHub release metadata we need.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// PlatformAsset maps GOOS/GOARCH to the expected Codex release asset name.
// Returns empty string for unsupported platforms.
func PlatformAsset(goos, goarch string) string {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "codex-x86_64-unknown-linux-musl.tar.gz"
	case "linux/arm64":
		return "codex-aarch64-unknown-linux-musl.tar.gz"
	case "darwin/arm64":
		return "codex-aarch64-apple-darwin.tar.gz"
	case "darwin/amd64":
		return "codex-x86_64-apple-darwin.tar.gz"
	case "windows/amd64":
		return "codex-x86_64-pc-windows-msvc.exe.tar.gz"
	case "windows/arm64":
		return "codex-aarch64-pc-windows-msvc.exe.tar.gz"
	default:
		return ""
	}
}

// BinaryPath returns the expected path to the codex executable for a given version.
func (m *Manager) BinaryPath(version string) string {
	bin := BinaryName
	if runtime.GOOS == "windows" {
		bin = BinaryNameWindows
	}
	return filepath.Join(m.versionDir(version), bin)
}

func (m *Manager) versionDir(version string) string {
	return filepath.Join(m.BaseDir, version)
}

func (m *Manager) manifestPath() string {
	return filepath.Join(m.BaseDir, ManifestName)
}

// LoadManifest reads the manifest from disk. Returns an empty manifest if
// the file does not exist.
func (m *Manager) LoadManifest() (*Manifest, error) {
	data, err := os.ReadFile(m.manifestPath())
	if os.IsNotExist(err) {
		return &Manifest{Installed: map[string]InstalledVer{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runtime: read manifest: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return nil, fmt.Errorf("runtime: parse manifest: %w", err)
	}
	if man.Installed == nil {
		man.Installed = map[string]InstalledVer{}
	}
	return &man, nil
}

// SaveManifest writes the manifest to disk.
func (m *Manager) SaveManifest(man *Manifest) error {
	if err := os.MkdirAll(m.BaseDir, 0o755); err != nil {
		return fmt.Errorf("runtime: create base dir: %w", err)
	}
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return fmt.Errorf("runtime: marshal manifest: %w", err)
	}
	return os.WriteFile(m.manifestPath(), data, 0o644)
}

// EnsureBinary checks if a compatible Codex binary is installed and returns
// its path. If no binary is installed, it downloads and installs the latest
// stable release.
//
// This is the main entry point for callers that need a Codex binary path.
func (m *Manager) EnsureBinary(ctx context.Context) (string, error) {
	man, err := m.LoadManifest()
	if err != nil {
		return "", err
	}

	// Check if active version exists and binary is present
	if man.ActiveVersion != "" {
		binPath := m.BinaryPath(man.ActiveVersion)
		if info, err := os.Stat(binPath); err == nil && !info.IsDir() {
			// Verify SHA256 against manifest to detect tampering or
			// corrupted downloads. If the hash is empty (legacy install
			// before verification was added), compute and persist it now
			// so subsequent calls can verify.
			ver, ok := man.Installed[man.ActiveVersion]
			if !ok {
				return binPath, nil
			}
			actual, err := fetch.FileSHA256(binPath)
			if err != nil {
				return "", fmt.Errorf("runtime: hash binary %s: %w", binPath, err)
			}
			if ver.SHA256 == "" {
				ver.SHA256 = actual
				man.Installed[man.ActiveVersion] = ver
				_ = m.SaveManifest(man)
				return binPath, nil
			}
			if !strings.EqualFold(ver.SHA256, actual) {
				return "", fmt.Errorf("runtime: binary %s SHA256 mismatch (expected %s, got %s) — possible tampering or corrupted download; remove the runtime dir to re-download", binPath, ver.SHA256, actual)
			}
			return binPath, nil
		}
	}

	// Need to download
	return m.DownloadLatest(ctx)
}

// Delete removes an installed Codex runtime version from disk and
// updates the manifest. If the deleted version was the active one,
// ActiveVersion is cleared so the next EnsureBinary call downloads
// the latest release.
func (m *Manager) Delete(version string) error {
	// Remove the version directory.
	versionDir := m.versionDir(version)
	if err := os.RemoveAll(versionDir); err != nil {
		return fmt.Errorf("runtime: delete version dir %s: %w", versionDir, err)
	}

	// Update manifest.
	man, err := m.LoadManifest()
	if err != nil {
		return fmt.Errorf("runtime: load manifest after delete: %w", err)
	}
	delete(man.Installed, version)
	if man.ActiveVersion == version {
		man.ActiveVersion = ""
	}
	if err := m.SaveManifest(man); err != nil {
		return fmt.Errorf("runtime: save manifest after delete: %w", err)
	}
	return nil
}

// DownloadLatest fetches and installs the latest Codex release.
// Returns the path to the installed binary.
func (m *Manager) DownloadLatest(ctx context.Context) (string, error) {
	assetName := PlatformAsset(runtime.GOOS, runtime.GOARCH)
	if assetName == "" {
		return "", fmt.Errorf("runtime: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// 1. Fetch release metadata from GitHub
	release, err := m.fetchLatestRelease(ctx)
	if err != nil {
		return "", fmt.Errorf("runtime: fetch latest release: %w", err)
	}

	version := parseVersionTag(release.TagName)
	if version == "" {
		return "", fmt.Errorf("runtime: cannot parse version from tag %q", release.TagName)
	}

	// 2. Find the matching asset
	var assetURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			assetURL = a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		return "", fmt.Errorf("runtime: asset %q not found in release %s", assetName, version)
	}

	// 3. Download and extract
	binPath, err := m.downloadAndExtract(ctx, version, assetURL)
	if err != nil {
		return "", err
	}

	// 4. Compute SHA256 for tamper detection on subsequent loads.
	hash, err := fetch.FileSHA256(binPath)
	if err != nil {
		return "", fmt.Errorf("runtime: hash downloaded binary: %w", err)
	}

	// 5. Update manifest
	man, _ := m.LoadManifest()
	if man.Installed == nil {
		man.Installed = map[string]InstalledVer{}
	}
	man.Installed[version] = InstalledVer{
		Path:        binPath,
		Source:      "github",
		InstalledAt: time.Now().UTC(),
		SHA256:      hash,
	}
	man.ActiveVersion = version
	if err := m.SaveManifest(man); err != nil {
		return "", err
	}

	return binPath, nil
}

// fetchLatestRelease calls the GitHub API to get the latest release metadata.
func (m *Manager) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	client := m.HTTPClient
	if client == nil {
		client = httpclient.NewWithTimeout(httpTimeout)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", GitHubAPILatest, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release metadata: %w", err)
	}
	return &release, nil
}

// downloadAndExtract downloads the tar.gz asset and extracts the codex binary
// into the versioned runtime directory.
func (m *Manager) downloadAndExtract(ctx context.Context, version, assetURL string) (string, error) {
	client := m.HTTPClient
	if client == nil {
		client = httpclient.NewWithTimeout(downloadTimeout)
	}

	body, err := fetch.Body(ctx, client, assetURL, nil)
	if err != nil {
		return "", fmt.Errorf("download asset: %w", err)
	}
	defer body.Close()

	// Create version directory
	versionDir := m.versionDir(version)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return "", fmt.Errorf("create version dir: %w", err)
	}

	binName := BinaryName
	if runtime.GOOS == "windows" {
		binName = BinaryNameWindows
	}
	binPath := filepath.Join(versionDir, binName)

	// Extract: the tar.gz contains the codex binary, named after the
	// platform target (e.g. "codex-x86_64-unknown-linux-musl").
	// We extract it and rename to "codex" / "codex.exe".
	var found bool
	err = archive.WalkTarGz(body, func(hdr *tar.Header, r io.Reader) (bool, error) {
		// The archive contains a single binary file. Accept any regular
		// file that starts with "codex" and is executable.
		name := filepath.Base(hdr.Name)
		if !strings.HasPrefix(name, "codex") {
			return true, nil
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return true, nil
		}

		out, err := os.OpenFile(binPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return false, fmt.Errorf("create binary: %w", err)
		}
		if _, err := io.Copy(out, r); err != nil {
			out.Close()
			return false, fmt.Errorf("write binary: %w", err)
		}
		out.Close()
		found = true
		return false, nil
	})
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no codex binary found in archive")
	}
	return binPath, nil
}

// parseVersionTag extracts the version string from a GitHub tag name.
// Tag names look like "rust-v0.147.0" or "v0.147.0".
// Returns "0.147.0" or empty string if parsing fails.
func parseVersionTag(tag string) string {
	s := tag
	s = strings.TrimPrefix(s, "rust-v")
	s = strings.TrimPrefix(s, "v")
	return s
}
