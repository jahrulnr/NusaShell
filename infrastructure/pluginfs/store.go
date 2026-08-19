// Package pluginfs manages installed plugins on the filesystem. Each
// plugin lives under <datadir>/plugins/<id>/ and contains manifest.json
// plus mcp/ and ui/ directories. The store scans the plugins directory
// and parses manifests on demand.
package pluginfs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/pluginicon"
)

// Store reads installed plugins from <root>/<plugin-id>/.
type Store struct {
	root string
}

// New creates a plugin store rooted at root. The directory is created
// if it does not exist.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("pluginfs: mkdir %s: %w", root, err)
	}
	return &Store{root: root}, nil
}

// Root returns the filesystem root for installed plugins.
func (s *Store) Root() string { return s.root }

// List scans the plugins directory and returns all valid plugins.
func (s *Store) List() ([]*domain.Plugin, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("pluginfs: read %s: %w", s.root, err)
	}
	var out []*domain.Plugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		p, err := s.Get(entry.Name())
		if err != nil {
			continue // skip broken plugins
		}
		out = append(out, p)
	}
	return out, nil
}

// safePluginDir validates id as a plugin identifier and returns the
// absolute plugin directory. It guards Get/Uninstall from path-traversal
// via a caller-supplied id (e.g. ".." or "/etc/passwd").
func (s *Store) safePluginDir(id string) (string, error) {
	if !domain.ValidatePluginID(id) {
		return "", fmt.Errorf("pluginfs: invalid plugin id %q", id)
	}
	dir := filepath.Join(s.root, id)
	// Defense-in-depth: ensure the resolved path stays under root.
	if !strings.HasPrefix(dir, s.root+string(filepath.Separator)) && dir != s.root {
		return "", fmt.Errorf("pluginfs: id %q escapes plugins root", id)
	}
	return dir, nil
}

// Get reads and validates a single plugin's manifest.
func (s *Store) Get(id string) (*domain.Plugin, error) {
	dir, err := s.safePluginDir(id)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("plugin %q not found", id)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("plugin %q is not a directory", id)
	}
	manifest, err := s.loadManifest(dir)
	if err != nil {
		return nil, err
	}
	return &domain.Plugin{
		Manifest:    *manifest,
		InstallPath: dir,
		HasUI:       manifest.HasWindow(),
		InstalledAt: info.ModTime(),
	}, nil
}

// Install copies a plugin directory into the store. The source must
// contain a valid manifest.json. If a plugin with the same ID exists,
// it is replaced.
func (s *Store) Install(sourceDir string) (*domain.Plugin, error) {
	manifest, err := s.loadManifest(sourceDir)
	if err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	sourcePath, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("pluginfs: resolve source %s: %w", sourceDir, err)
	}
	rootPath, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return nil, fmt.Errorf("pluginfs: resolve root %s: %w", s.root, err)
	}
	if rel, err := filepath.Rel(rootPath, sourcePath); err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
		return nil, fmt.Errorf("pluginfs: source directory must be outside the installed plugins root: %s", sourceDir)
	}

	destDir := filepath.Join(s.root, manifest.ID)
	// Remove existing plugin with same ID.
	if _, statErr := os.Stat(destDir); statErr == nil {
		if err := os.RemoveAll(destDir); err != nil {
			return nil, fmt.Errorf("pluginfs: remove old %s: %w", destDir, err)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("pluginfs: stat destination %s: %w", destDir, statErr)
	}
	if err := copyDir(sourceDir, destDir); err != nil {
		return nil, fmt.Errorf("pluginfs: copy %s → %s: %w", sourceDir, destDir, err)
	}

	return s.Get(manifest.ID)
}

// Uninstall removes a plugin directory.
func (s *Store) Uninstall(id string) error {
	dir, err := s.safePluginDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("plugin %q not found", id)
	}
	return os.RemoveAll(dir)
}

// Save writes a plugin's manifest.json back into its directory, creating
// the directory if needed. It is used by the manual plugin editor so
// user-created MCP servers are stored the same way as installed plugins.
func (s *Store) Save(p *domain.Plugin) error {
	if p == nil || p.Manifest.ID == "" {
		return fmt.Errorf("pluginfs: cannot save plugin without id")
	}
	dir, err := s.safePluginDir(p.Manifest.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pluginfs: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(p.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("pluginfs: marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		return fmt.Errorf("pluginfs: write manifest: %w", err)
	}
	return nil
}

// Delete removes a plugin directory. Manual MCP-server-as-plugin entries
// and catalog-installed plugins are removed the same way.
func (s *Store) Delete(id string) error {
	return s.Uninstall(id)
}

// UIPath returns the absolute path to the plugin's UI entry file.
func (s *Store) UIPath(p *domain.Plugin) string {
	if p.Manifest.UI == nil {
		return ""
	}
	return filepath.Join(p.InstallPath, p.Manifest.UI.Entry)
}

// UIDir returns the directory containing the UI entry file.
func (s *Store) UIDir(p *domain.Plugin) string {
	uiPath := s.UIPath(p)
	if uiPath == "" {
		return ""
	}
	return filepath.Dir(uiPath)
}

// loadManifest reads and parses manifest.json from a plugin directory.
func (s *Store) loadManifest(dir string) (*domain.PluginManifest, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("pluginfs: read manifest.json in %s: %w", dir, err)
	}
	var m domain.PluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("pluginfs: parse manifest.json in %s: %w", dir, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// --- helpers ---

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		// Skip .git directories and symlinks. Symlinks in node_modules/.bin
		// are not portable and .git is never useful at runtime.
		if info.Mode()&os.ModeSymlink != 0 || rel == ".git" || strings.Contains(rel, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dest := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, info.Mode().Perm())
	})
}

// IsInstalled returns true if a plugin with the given ID exists.
func (s *Store) IsInstalled(id string) bool {
	dir := filepath.Join(s.root, id)
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// PluginDTO converts a domain.Plugin to a lightweight DTO for transport.
type PluginDTO struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Icon        string                 `json:"icon"`
	Category    string                 `json:"category,omitempty"`
	HasUI       bool                   `json:"hasUI"`
	InstallPath string                 `json:"installPath"`
	InstalledAt time.Time              `json:"installedAt"`
	Manifest    *domain.PluginManifest `json:"manifest,omitempty"`
}

// ToDTO converts a domain.Plugin to a PluginDTO. Local file icons are
// resolved to PNG data URLs so browsers (http://localhost) can render them
// (file:// is blocked by the browser from an http origin).
func ToDTO(p *domain.Plugin) PluginDTO {
	return PluginDTO{
		ID:          p.Manifest.ID,
		Name:        p.Manifest.Name,
		Version:     p.Manifest.Version,
		Icon:        pluginicon.ResolveLocal(p.Manifest.Icon, p.InstallPath),
		Category:    p.Manifest.Category,
		HasUI:       p.HasUI,
		InstallPath: p.InstallPath,
		InstalledAt: p.InstalledAt,
		Manifest:    &p.Manifest,
	}
}
