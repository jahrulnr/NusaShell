package petsinstall

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"nusashell/infrastructure/nusatemp"
)

// Default release stream coordinates. NUSASHELL_RELEASE_BASE /
// NUSASHELL_RELEASE_INDEX override these via NewWithOverrides so tests can
// point at a sandbox.
const (
	defaultReleaseBase  = "https://github.com/jahrulnr/NusaShell/releases"
	defaultReleaseIndex = "https://raw.githubusercontent.com/jahrulnr/NusaShell/master/release-versions.json"
)

// Phase labels ride the pets.install.* events so the dialog can label the
// bar without hard-coding the installer phases.
const (
	PhaseResolve  = "resolve"
	PhaseDownload = "download"
	PhaseVerify   = "verify"
	PhaseExtract  = "extract"
	PhaseActivate = "activate"
	PhaseLauncher = "launcher"
)

// Progress is the installer-side progress callback payload. The adapter
// translates it into contracts.PetsInstallProgressDTO.
type Progress struct {
	Phase        string
	BytesFetched int64
	BytesTotal   int64
	Message      string
}

// HTTPClient is the subset of *http.Client the installer relies on. Tests
// inject a stub to avoid hitting the network.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Installer resolves the pets release stream, downloads the matched asset,
// verifies its SHA-256, extracts into a versioned root, and activates the
// "current" symlink. status.go owns the on-disk probe that backs the wire
// status RPC.
type Installer struct {
	resolver     *Resolver
	releaseBase  string
	releaseIndex string
	httpClient   HTTPClient
}

// New builds a production installer rooted at the caller's home directory.
func New(homeDir string) *Installer {
	return NewWithOverrides(homeDir, defaultReleaseBase, defaultReleaseIndex, &http.Client{Timeout: 30 * time.Minute})
}

// NewWithOverrides injects release coordinates + HTTP client. Used by tests.
func NewWithOverrides(homeDir, releaseBase, releaseIndex string, client HTTPClient) *Installer {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	return &Installer{
		resolver:     DefaultResolver(homeDir),
		releaseBase:  strings.TrimRight(releaseBase, "/"),
		releaseIndex: releaseIndex,
		httpClient:   client,
	}
}

// NewWithResolver builds an installer around a fully custom Resolver. The
// resolver's Home + Env + LookPath + ProcRoot drive every probe; release
// coordinates still come from the constructor args.
func NewWithResolver(resolver *Resolver, releaseBase, releaseIndex string, client HTTPClient) *Installer {
	if resolver == nil {
		resolver = DefaultResolver("")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	return &Installer{
		resolver:     resolver,
		releaseBase:  strings.TrimRight(releaseBase, "/"),
		releaseIndex: releaseIndex,
		httpClient:   client,
	}
}

// Resolver exposes the underlying probe so callers (and the adapter) can
// run ad-hoc queries beyond the Status() snapshot.
func (in *Installer) Resolver() *Resolver { return in.resolver }

// StatusResult mirrors contracts.PetsStatusResult (kept here to avoid
// importing contracts; the adapter translates one-to-one).
type StatusResult struct {
	Supported   bool   `json:"supported"`
	Installed   bool   `json:"installed"`
	Path        string `json:"path,omitempty"`
	AssetsPath  string `json:"assets_path,omitempty"`
	Version     string `json:"version,omitempty"`
	InstallRoot string `json:"install_root,omitempty"`
	Launcher    string `json:"launcher,omitempty"`
	Running     bool   `json:"running"`
}

// Status snapshots the install state right now.
func (in *Installer) Status() StatusResult {
	r := in.resolver
	res := StatusResult{Supported: runtime.GOOS == "linux"}
	res.InstallRoot = r.Env("NUSASHELL_PETS_INSTALL_ROOT")
	if res.InstallRoot == "" {
		res.InstallRoot = filepath.Join(r.Home, ".local/share/nusashell-pets")
	}
	if path, ok := r.PetsBinary(); ok {
		res.Installed = true
		res.Path = path
		res.AssetsPath = r.PetsAssetsPath()
		res.Version = r.PetsInstalledVersion()
	}
	launcher := r.PetsLauncher()
	if launcher != "" {
		if info, err := os.Stat(launcher); err == nil && info.Mode().Perm()&0o111 != 0 {
			res.Launcher = launcher
		}
	}
	res.Running = r.PetsRunning()
	return res
}

// install runs the resolve → download → verify → extract → activate →
// launcher pipeline. report carries progress events to the application
// layer (translated to contracts.PetsInstallProgressDTO by adapter.go).
// version="" resolves the latest published release.
func (in *Installer) install(ctx context.Context, version string, report func(Progress)) error {
	if report == nil {
		report = func(Progress) {}
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("petsinstall: desktop pet is Linux only")
	}
	if in.resolver == nil {
		return fmt.Errorf("petsinstall: resolver not configured")
	}

	report(Progress{Phase: PhaseResolve, Message: "Resolving pets release stream"})
	stream, err := in.resolveStream(ctx, version)
	if err != nil {
		return fmt.Errorf("petsinstall: resolve: %w", err)
	}

	root := filepath.Join(in.resolver.Home, ".local/share/nusashell-pets")
	if override := in.resolver.Env("NUSASHELL_PETS_INSTALL_ROOT"); override != "" {
		root = override
	}
	versionDir := filepath.Join(root, "versions", stream.Version)
	if err := os.MkdirAll(filepath.Dir(versionDir), 0o755); err != nil {
		return fmt.Errorf("petsinstall: layout: %w", err)
	}

	// If the versioned root already has the binary + assets, skip every
	// network round-trip: the manifest, SHA-256 check, and tar extraction
	// only matter on the first install of a version.
	if !in.versionReady(versionDir) {
		report(Progress{Phase: PhaseResolve, Message: "Resolving pets release manifest"})
		asset, err := in.resolveAsset(ctx, stream)
		if err != nil {
			return fmt.Errorf("petsinstall: resolve: %w", err)
		}

		report(Progress{Phase: PhaseDownload, Message: "Downloading pets release " + stream.Version})
		staging, err := in.download(ctx, asset)
		if err != nil {
			return fmt.Errorf("petsinstall: download: %w", err)
		}
		defer os.RemoveAll(filepath.Dir(staging))

		report(Progress{Phase: PhaseVerify, Message: "Verifying SHA-256"})
		if err := verifySHA256(staging, asset.SHA256); err != nil {
			return fmt.Errorf("petsinstall: verify: %w", err)
		}

		report(Progress{Phase: PhaseExtract, Message: "Extracting pets release"})
		if err := os.MkdirAll(versionDir, 0o755); err != nil {
			return fmt.Errorf("petsinstall: extract: %w", err)
		}
		if err := extractTarGz(staging, versionDir); err != nil {
			return fmt.Errorf("petsinstall: extract: %w", err)
		}
	}

	report(Progress{Phase: PhaseActivate, Message: "Activating " + stream.Version})
	if err := activateVersion(root, stream.Version); err != nil {
		return fmt.Errorf("petsinstall: activate: %w", err)
	}

	report(Progress{Phase: PhaseLauncher, Message: "Writing launcher"})
	if err := writeLauncher(in.resolver); err != nil {
		return fmt.Errorf("petsinstall: launcher: %w", err)
	}
	if err := writeDesktopEntry(in.resolver, versionDir); err != nil {
		// Desktop entry is best-effort; missing it does not break the binary.
		report(Progress{Phase: PhaseLauncher, Message: "Desktop entry skipped: " + err.Error()})
	}

	report(Progress{Phase: PhaseVerify, Message: "Desktop pet ready"})
	return nil
}

// Launch spawns the resolved pet binary in the background. Returns the
// absolute path it ran, or a non-nil error when the binary cannot be
// resolved or spawned. The process is detached so the Go server can exit
// without killing the pet.
func (in *Installer) Launch() (string, error) {
	path, ok := in.resolver.PetsBinary()
	if !ok {
		return "", fmt.Errorf("petsinstall: binary not resolved")
	}
	assets := in.resolver.PetsAssetsPath()
	if assets == "" {
		assets = filepath.Join(filepath.Dir(filepath.Dir(path)), "assets/pets")
	}
	cmd := exec.Command(path, "--assets", assets)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("petsinstall: spawn: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return path, nil
}

// ---- release stream + asset resolution ----

type stream struct {
	Version  string
	Tag      string
	Manifest string
}

type asset struct {
	Name   string
	SHA256 string
	URL    string
}

func (in *Installer) resolveStream(ctx context.Context, version string) (*stream, error) {
	if version != "" {
		if !semverRe.MatchString(version) {
			return nil, fmt.Errorf("invalid pets version %q", version)
		}
		return &stream{Version: version, Tag: "pets-v" + version, Manifest: "pets-latest.json"}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.releaseIndex, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch release index: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetch release index: %w", err)
	}
	var doc struct {
		Pets struct {
			Version  string `json:"version"`
			Tag      string `json:"tag"`
			Manifest string `json:"manifest"`
		} `json:"pets"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse release index: %w", err)
	}
	if doc.Pets.Version == "" || doc.Pets.Tag == "" {
		return nil, fmt.Errorf("release index missing pets pointer")
	}
	if doc.Pets.Manifest == "" {
		doc.Pets.Manifest = "pets-latest.json"
	}
	if !semverRe.MatchString(doc.Pets.Version) {
		return nil, fmt.Errorf("release index contains invalid pets version %q", doc.Pets.Version)
	}
	if doc.Pets.Tag != "pets-v"+doc.Pets.Version {
		return nil, fmt.Errorf("release index contains invalid pets tag %q", doc.Pets.Tag)
	}
	if doc.Pets.Manifest != "pets-latest.json" {
		return nil, fmt.Errorf("release index contains invalid pets manifest %q", doc.Pets.Manifest)
	}
	return &stream{Version: doc.Pets.Version, Tag: doc.Pets.Tag, Manifest: doc.Pets.Manifest}, nil
}

// resolveAsset fetches pets-latest.json and picks the linux/arch entry.
func (in *Installer) resolveAsset(ctx context.Context, s *stream) (*asset, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("desktop pet is Linux only (current: %s)", runtime.GOOS)
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "arm64":
		// keep as-is
	default:
		return nil, fmt.Errorf("no pets release for %s/%s", runtime.GOOS, arch)
	}
	url := fmt.Sprintf("%s/download/%s/%s", in.releaseBase, s.Tag, s.Manifest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", s.Manifest, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", s.Manifest, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	key := runtime.GOOS + "-" + arch
	// The release manifest shape mirrors release-manifest.mjs:
	//
	//   { "product": "pets", "version": "0.2.0",
	//     "files": { "linux-x64": { "name": "...", "sha256": "..." } } }
	//
	// Decode the envelope first and only unmarshal the platform entry, so
	// the string-typed top-level keys (product/version) never collide with
	// the per-platform object shape.
	var manifestDoc struct {
		Files map[string]struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &manifestDoc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Manifest, err)
	}
	entry, ok := manifestDoc.Files[key]
	if !ok || entry.Name == "" {
		return nil, fmt.Errorf("no pets release entry for %s", key)
	}
	if !safeArchiveName(entry.Name) {
		return nil, fmt.Errorf("unsafe release payload name %q", entry.Name)
	}
	return &asset{
		Name:   entry.Name,
		SHA256: strings.ToLower(entry.SHA256),
		URL:    fmt.Sprintf("%s/download/%s/%s", in.releaseBase, s.Tag, entry.Name),
	}, nil
}

func (in *Installer) download(ctx context.Context, a *asset) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := in.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d from %s", resp.StatusCode, a.URL)
	}
	tmp, err := nusatemp.MkdirTemp("pets-*")
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(tmp, "payload-*")
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.RemoveAll(tmp)
		return "", fmt.Errorf("download interrupted: %w", err)
	}
	if err := f.Close(); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return f.Name(), nil
}

func verifySHA256(path, expected string) error {
	if expected == "" {
		return fmt.Errorf("manifest SHA-256 missing")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("SHA-256 mismatch: got %s want %s", got, expected)
	}
	return nil
}

// extractTarGz unpacks every entry whose path passes the zip-slip guard.
func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("bad archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("bad archive: %w", err)
		}
		name := filepath.FromSlash(hdr.Name)
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("unsafe archive entry %q", hdr.Name)
		}
		target := filepath.Join(dest, name)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanDest) {
			return fmt.Errorf("archive escapes dest: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			// Skip symlinks and other exotic entries; release payloads only
			// contain dirs + regular files.
		}
	}
}

func (in *Installer) versionReady(versionDir string) bool {
	bin := filepath.Join(versionDir, "nusashell-pets")
	if !in.resolver.executable(bin) {
		return false
	}
	if _, err := os.Stat(filepath.Join(versionDir, "assets/pets/config.json")); err != nil {
		return false
	}
	return true
}

// activateVersion swaps the active symlink to the given version using a
// temp symlink + rename for atomicity on POSIX.
func activateVersion(root, version string) error {
	versions := filepath.Join(root, "versions")
	current := filepath.Join(root, "current")
	target := filepath.Join(versions, version)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("activate: version dir missing: %w", err)
	}
	tmpLink := filepath.Join(root, ".current-"+version)
	if err := os.Symlink(target, tmpLink); err != nil {
		if rmErr := os.Remove(current); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("activate: remove current: %w", rmErr)
		}
		return os.Symlink(target, current)
	}
	if err := os.Rename(tmpLink, current); err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("activate: rename: %w", err)
	}
	if err := os.Chmod(filepath.Join(target, "nusashell-pets"), 0o755); err != nil {
		return fmt.Errorf("activate: chmod: %w", err)
	}
	return nil
}

// writeLauncher creates ~/.local/bin/nusashell-pets pointing at the
// active binary's --assets path.
func writeLauncher(r *Resolver) error {
	bin := r.PetsLauncher()
	if bin == "" {
		return fmt.Errorf("writeLauncher: launcher path unresolved")
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	installRoot := r.Env("NUSASHELL_PETS_INSTALL_ROOT")
	if installRoot == "" {
		installRoot = filepath.Join(r.Home, ".local/share/nusashell-pets")
	}
	active := filepath.Join(installRoot, "current")
	assets := filepath.Join(active, "assets/pets")
	body := fmt.Sprintf("#!/usr/bin/env sh\nexec %q --assets %q \"$@\"\n", filepath.Join(active, "nusashell-pets"), assets)
	return os.WriteFile(bin, []byte(body), 0o755)
}

// writeDesktopEntry mirrors scripts/install.sh. Errors here are non-fatal.
func writeDesktopEntry(r *Resolver, versionDir string) error {
	if r.Home == "" {
		return fmt.Errorf("writeDesktopEntry: home unresolved")
	}
	dir := filepath.Join(r.Home, ".local/share/applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	installRoot := r.Env("NUSASHELL_PETS_INSTALL_ROOT")
	if installRoot == "" {
		installRoot = filepath.Join(r.Home, ".local/share/nusashell-pets")
	}
	icon := filepath.Join(installRoot, "current/resources/nusashell.png")
	entry := filepath.Join(dir, "nusashell-pets.desktop")
	launcher := r.PetsLauncher()
	body := []string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=NusaShell Pets",
		"Comment=NusaShell desktop pet",
		"Exec=" + launcher,
		"Terminal=false",
		"Categories=Utility;Game;",
	}
	if _, err := os.Stat(icon); err == nil {
		body = append(body, "Icon="+icon)
	}
	return os.WriteFile(entry, []byte(strings.Join(body, "\n")+"\n"), 0o644)
}

// semverRe matches the bash installer validator.
var semverRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$`)

// safeArchiveNameRe guards against path traversal in release payload names.
var safeArchiveNameRe = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

func safeArchiveName(name string) bool {
	return name != "" && name == filepath.Base(name) && safeArchiveNameRe.MatchString(name)
}
