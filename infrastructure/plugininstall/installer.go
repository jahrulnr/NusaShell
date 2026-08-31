package plugininstall

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/nusatemp"
	"nusashell/infrastructure/pluginicon"
	clock "nusashell/pkg/time"
)

// Installer fetches and installs plugins from the curated catalog, a GitHub
// repository, or a local ZIP archive. It depends on a plugin store for the
// final copy and validation.
type Installer struct {
	store pluginStore

	logger      *slog.Logger
	httpClient  *http.Client
	rawBaseURL  string // raw github content root (catalog manifests, default repo files)
	releaseBase string // github release download root
	githubBase  string // github archive root

	mu           sync.Mutex
	catalogCache []domain.PluginCatalogEntry
	catalogAt    time.Time
}

type pluginStore interface {
	Install(sourceDir string) (*domain.Plugin, error)
}

// New creates an installer that reads the curated catalog from
// github.com/jahrulnr/NusaShell-mcp and installs into store.
func New(store pluginStore, logger *slog.Logger) *Installer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Installer{
		store:  store,
		logger: logger,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		rawBaseURL:  "https://raw.githubusercontent.com/jahrulnr/NusaShell-mcp/master",
		releaseBase: "https://github.com/jahrulnr/NusaShell-mcp/releases/download",
		githubBase:  "https://github.com",
	}
}

// Catalog returns the curated first-party plugin catalog. The catalog is
// cached for five minutes to avoid hammering GitHub on every dialog open.
func (i *Installer) Catalog(ctx context.Context) ([]domain.PluginCatalogEntry, error) {
	i.mu.Lock()
	if i.catalogCache != nil && clock.NewTime().Since(i.catalogAt) < 5*time.Minute {
		out := i.catalogCache
		i.mu.Unlock()
		return out, nil
	}
	i.mu.Unlock()

	versions, err := i.fetchVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("plugin catalog: %w", err)
	}

	keys := make([]string, 0, len(versions))
	for k := range versions {
		if k == "mail" {
			continue // mail is out of scope for the Go port
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		result []domain.PluginCatalogEntry
	)

	for _, key := range keys {
		info := versions[key]
		wg.Add(1)
		go func(key string, info catalogVersion) {
			defer wg.Done()
			manifestURL := i.rawBaseURL + "/" + key + "/manifest.json"
			body, err := i.fetch(ctx, manifestURL)
			if err != nil {
				i.logger.Warn("plugin catalog: skipping entry", "id", key, "error", err)
				return
			}
			var manifest domain.PluginManifest
			if err := json.NewDecoder(body).Decode(&manifest); err != nil {
				body.Close()
				i.logger.Warn("plugin catalog: skipping entry", "id", key, "error", err)
				return
			}
			body.Close()
			if err := manifest.Validate(); err != nil {
				i.logger.Warn("plugin catalog: skipping entry", "id", key, "error", err)
				return
			}
			entry := domain.PluginCatalogEntry{
				ID:          key,
				PluginID:    manifest.ID,
				Name:        manifest.Name,
				Version:     info.Version,
				Description: manifest.Description,
				Icon:        i.resolveCatalogIcon(ctx, key, manifest.Icon),
				Tag:         info.Tag,
				ReleasedAt:  info.ReleasedAt,
			}
			mu.Lock()
			result = append(result, entry)
			mu.Unlock()
		}(key, info)
	}

	wg.Wait()

	i.mu.Lock()
	i.catalogCache = result
	i.catalogAt = clock.NewTime().Time()
	i.mu.Unlock()

	return result, nil
}

// CheckUpdates returns catalog entries that have a newer version than the
// installed plugin with the same plugin id.
func (i *Installer) CheckUpdates(ctx context.Context, installed []*domain.Plugin) ([]domain.PluginCatalogEntry, error) {
	catalog, err := i.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*domain.Plugin{}
	for _, p := range installed {
		byID[p.Manifest.ID] = p
	}
	var updates []domain.PluginCatalogEntry
	for _, entry := range catalog {
		installed, ok := byID[entry.PluginID]
		if !ok {
			continue // not installed -> not an "update"
		}
		if newerVersion(entry.Version, installed.Manifest.Version) {
			updates = append(updates, entry)
		}
	}
	return updates, nil
}

// Update reinstalls a catalog plugin from its latest release (the plugin
// store replaces the existing folder with the same id).
func (i *Installer) Update(ctx context.Context, pluginID string) (*domain.Plugin, error) {
	return i.Install(ctx, domain.PluginInstallRequest{
		Source: domain.InstallSourceCatalog,
		ID:     pluginID,
	})
}

// newerVersion reports whether cand is a newer semantic version than base.
// Non-semver versions fall back to simple string inequality ordering.
func newerVersion(cand, base string) bool {
	a, aok := parseSemver(cand)
	b, bok := parseSemver(base)
	if aok && bok {
		if a[0] != b[0] {
			return a[0] > b[0]
		}
		if a[1] != b[1] {
			return a[1] > b[1]
		}
		return a[2] > b[2]
	}
	return cand != "" && base != "" && cand != base
}

// parseSemver parses "major.minor.patch" (ignoring prerelease suffix).
func parseSemver(v string) ([3]int, bool) {
	core := v
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// Install fetches a plugin from the requested source, validates it, and
// installs it through the store.
func (i *Installer) Install(ctx context.Context, req domain.PluginInstallRequest) (*domain.Plugin, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	stage, err := nusatemp.MkdirTemp("plugin-*")
	if err != nil {
		return nil, fmt.Errorf("plugin install: create stage: %w", err)
	}
	defer os.RemoveAll(stage)

	sourceDir := ""
	switch req.Source {
	case domain.InstallSourceCatalog:
		sourceDir, err = i.installFromCatalog(ctx, req.ID, stage)
	case domain.InstallSourceGitHub:
		sourceDir, err = i.installFromGitHub(ctx, req.URL, req.Subdir, req.Ref, stage)
	case domain.InstallSourceZip:
		sourceDir, err = i.installFromZip(req.Data, stage)
	default:
		return nil, fmt.Errorf("plugin install: unsupported source %q", req.Source)
	}
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "manifest.json")); err != nil {
		return nil, fmt.Errorf("plugin install: no manifest.json in %s", sourceDir)
	}

	return i.store.Install(sourceDir)
}

func (i *Installer) installFromCatalog(ctx context.Context, id, stage string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("plugin install: catalog id is required")
	}

	versions, err := i.fetchVersions(ctx)
	if err != nil {
		return "", err
	}
	info, ok := versions[id]
	if !ok {
		return "", fmt.Errorf("plugin install: %q is not in the catalog", id)
	}
	if id == "mail" {
		return "", fmt.Errorf("plugin install: the mail plugin is not supported by the Go port")
	}

	assetName := fmt.Sprintf("%s-%s.tar.gz", id, info.Version)
	assetURL := fmt.Sprintf("%s/%s/%s", i.releaseBase, info.Tag, assetName)

	body, err := i.fetch(ctx, assetURL)
	if err != nil {
		return "", fmt.Errorf("plugin install: download release: %w", err)
	}
	defer body.Close()

	if err := extractTarGz(body, stage); err != nil {
		return "", fmt.Errorf("plugin install: extract release: %w", err)
	}

	return findUniqueManifestDir(stage)
}

func (i *Installer) installFromGitHub(ctx context.Context, rawURL, subdir, ref, stage string) (string, error) {
	owner, repo, urlRef, urlSubdir, err := parseGitHubURL(rawURL)
	if err != nil {
		return "", fmt.Errorf("plugin install: %w", err)
	}
	if ref == "" {
		ref = urlRef
	}
	if ref == "" {
		ref = "HEAD"
	}
	if subdir == "" {
		subdir = urlSubdir
	}

	archiveURL := fmt.Sprintf("%s/%s/%s/archive/%s.tar.gz", i.githubBase, owner, repo, url.PathEscape(ref))
	body, err := i.fetch(ctx, archiveURL)
	if err != nil {
		return "", fmt.Errorf("plugin install: download repo: %w", err)
	}
	defer body.Close()

	if err := extractTarGz(body, stage); err != nil {
		return "", fmt.Errorf("plugin install: extract repo: %w", err)
	}

	entries, err := os.ReadDir(stage)
	if err != nil {
		return "", err
	}
	var topDir string
	for _, e := range entries {
		if e.IsDir() {
			if topDir != "" {
				return "", fmt.Errorf("archive has multiple top-level directories")
			}
			topDir = filepath.Join(stage, e.Name())
		}
	}
	if topDir == "" {
		return "", fmt.Errorf("archive has no top-level directory")
	}

	if subdir != "" {
		subdir = path.Clean("/" + subdir)
		sourceDir := filepath.Join(topDir, subdir)
		if _, err := os.Stat(filepath.Join(sourceDir, "manifest.json")); err != nil {
			return "", fmt.Errorf("plugin install: no manifest.json in %q", subdir)
		}
		return sourceDir, nil
	}

	return findUniqueManifestDir(topDir)
}

func (i *Installer) installFromZip(data []byte, stage string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("plugin install: zip data is empty")
	}

	zipPath := filepath.Join(stage, "upload.zip")
	if err := os.WriteFile(zipPath, data, 0o600); err != nil {
		return "", fmt.Errorf("plugin install: write zip: %w", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("plugin install: open zip: %w", err)
	}
	defer zr.Close()

	extractDir := filepath.Join(stage, "zip")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return "", err
	}
	if err := extractZip(&zr.Reader, extractDir); err != nil {
		return "", fmt.Errorf("plugin install: extract zip: %w", err)
	}

	return findUniqueManifestDir(extractDir)
}

func (i *Installer) fetch(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream, application/json, */*")

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	return resp.Body, nil
}

type catalogVersion struct {
	Version    string `json:"version"`
	Tag        string `json:"tag"`
	ReleasedAt string `json:"releasedAt"`
}

func (i *Installer) fetchVersions(ctx context.Context) (map[string]catalogVersion, error) {
	body, err := i.fetch(ctx, i.rawBaseURL+"/versions.json")
	if err != nil {
		return nil, fmt.Errorf("fetch versions: %w", err)
	}
	defer body.Close()

	var versions map[string]catalogVersion
	if err := json.NewDecoder(body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("parse versions: %w", err)
	}
	return versions, nil
}

// resolveCatalogIcon turns a manifest icon into a displayable value for
// catalog entries. Text/emoji and http(s) icons pass through; file-style
// icons are fetched from the raw catalog repo and embedded as PNG data
// URLs, falling back to a placeholder when the file is missing or invalid.
func (i *Installer) resolveCatalogIcon(ctx context.Context, key, icon string) string {
	if pluginicon.IsRemoteURL(icon) {
		return icon
	}
	rel := pluginicon.IconPath(icon)
	if rel == "" {
		return icon
	}
	body, err := i.fetch(ctx, i.rawBaseURL+"/"+key+"/"+rel)
	if err != nil {
		i.logger.Warn("plugin catalog: icon fetch failed", "id", key, "icon", icon, "error", err)
		return pluginicon.FallbackIcon
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, pluginicon.MaxIconBytes+1))
	if err != nil || !pluginicon.IsPNG(data) {
		return pluginicon.FallbackIcon
	}
	return pluginicon.DataURL(data)
}

func extractTarGz(rc io.ReadCloser, dest string) error {
	gr, err := gzip.NewReader(rc)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := validateArchivePath(dest, h.Name); err != nil {
			return err
		}

		target, err := secureJoin(dest, h.Name)
		if err != nil {
			return err
		}

		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode).Perm())
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Skip hard/symlinks — release tarballs do not need them.
		default:
			// Ignore device nodes, etc.
		}
	}
	return nil
}

func extractZip(zr *zip.Reader, dest string) error {
	for _, f := range zr.File {
		if err := validateArchivePath(dest, f.Name); err != nil {
			return err
		}
		target, err := secureJoin(dest, f.Name)
		if err != nil {
			return err
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return err
			}
			continue
		}

		// Skip symlinks in uploaded zips.
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func validateArchivePath(root, name string) error {
	if name == "" {
		return nil
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("archive path contains parent traversal: %s", name)
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("archive path is absolute: %s", name)
	}
	return nil
}

func secureJoin(root, name string) (string, error) {
	joined := filepath.Join(root, filepath.FromSlash(name))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absJoined, absRoot+string(filepath.Separator)) && absJoined != absRoot {
		return "", fmt.Errorf("archive path escapes root: %s", name)
	}
	return absJoined, nil
}

func findUniqueManifestDir(root string) (string, error) {
	var candidates []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "manifest.json" {
			candidates = append(candidates, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no manifest.json found in archive")
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple manifest.json found; use a subdir for monorepos")
	}
}

var githubURLRe = regexp.MustCompile(`^(?:(?:https?://)?(?:www\.)?github\.com/)?([^/\s]+)/([^/\s]+?)/?(?:/(?:tree|blob)/([^/\s]+)(?:/(.*))?)?$`)

func parseGitHubURL(raw string) (owner, repo, ref, subdir string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", "", fmt.Errorf("github URL is required")
	}

	m := githubURLRe.FindStringSubmatch(raw)
	if m == nil {
		return "", "", "", "", fmt.Errorf("invalid GitHub URL %q", raw)
	}

	owner = m[1]
	repo = strings.TrimSuffix(m[2], ".git")
	if len(m) > 3 {
		ref = m[3]
	}
	if len(m) > 4 {
		subdir = m[4]
	}

	// If the subpath ended in a file, walk up to its directory.
	if subdir != "" {
		s := path.Clean("/" + subdir)
		if strings.Contains(s, ".") {
			// Last segment looks like a file; take its directory.
			dir := path.Dir(s)
			if dir != "/" {
				subdir = strings.TrimPrefix(dir, "/")
			} else {
				subdir = ""
			}
		} else {
			subdir = strings.TrimPrefix(s, "/")
		}
	}

	return owner, repo, ref, subdir, nil
}
