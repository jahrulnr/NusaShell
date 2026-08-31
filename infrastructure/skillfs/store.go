// Package skillfs implements a filesystem-backed skill store that mirrors
// NusaShell Electron's skill architecture:
//
//   - Builtin skills are embedded in the binary (resources/agent/skills/)
//     and seeded into the user data directory on startup.
//   - Skill content (SKILL.md + support files) lives on the filesystem
//     under <datadir>/skills/<name>/.
//   - Skill metadata (state, origin, usage, pinned) is cataloged in
//     skills.json alongside the other JSON stores.
//   - Provenance is tracked in .provenance.json per skill directory.
//   - User-deleted builtin skills are recorded in .deleted-builtin.json
//     so they are not re-seeded on restart.
//
// The SkillStore interface is unchanged — List/Get/Save/Delete — but
// content is read from and written to the filesystem, not skills.json.
// skills.json only stores metadata (the Content field is always loaded
// from SKILL.md at read time).
package skillfs

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nusashell/application"
	"nusashell/domain"
	clock "nusashell/pkg/time"
	"nusashell/resources"
)

var skillIDRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Store implements application.SkillStore backed by the filesystem.
// Skill content (SKILL.md) is read from <root>/<name>/SKILL.md. Metadata
// (state, origin, usage, pinned) is persisted in skills.json via the
// embedded jsonstore. Content is never stored in skills.json — only
// cataloged metadata.
type Store struct {
	root string // <datadir>/skills
	json *jsonMetaStore
	mu   sync.RWMutex
	// pluginMounts maps "plugin:<pluginID>" → skills directory path.
	// Skills in these directories are read-only and mounted (no copy).
	pluginMounts map[string]string
}

// New creates a filesystem skill store rooted at root. The directory is
// created if it does not exist.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("skillfs: mkdir %s: %w", root, err)
	}
	meta := &jsonMetaStore{
		path:  filepath.Join(root, "skills.json"),
		items: make(map[string]*skillMeta),
	}
	if data, err := os.ReadFile(meta.path); err == nil {
		_ = json.Unmarshal(data, &meta.items)
	}
	return &Store{root: root, json: meta, pluginMounts: make(map[string]string)}, nil
}

// SeedBuiltinSkills copies builtin skill packages from the embedded
// resources/agent/skills/ tree into the user data directory. Skills the
// user intentionally deleted (listed in .deleted-builtin.json) are
// skipped. Existing builtin skills are overwritten so updates propagate.
// User/agent-owned skills are never touched.
func SeedBuiltinSkills(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("skillfs: mkdir %s: %w", root, err)
	}
	deleted, err := loadDeletedBuiltin(root)
	if err != nil {
		return err
	}
	provenance, err := loadProvenance(root)
	if err != nil {
		return err
	}

	// Walk the embedded skills tree.
	entries, err := resources.BuiltinSkillsFS.ReadDir("agent/skills")
	if err != nil {
		return fmt.Errorf("skillfs: read embedded skills: %w", err)
	}
	sourceIDs := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() || !skillIDRe.MatchString(entry.Name()) {
			continue
		}
		skillID := entry.Name()
		sourceIDs[skillID] = true

		// Skip user-deleted builtins.
		if _, deleted := deleted[skillID]; deleted {
			continue
		}

		// Skip if the skill exists and is not builtin-origin (user or
		// agent created it — don't overwrite their version).
		destDir := filepath.Join(root, skillID)
		if origin, ok := provenance[skillID]; ok && origin.CreatedBy != "builtin" {
			if _, err := os.Stat(filepath.Join(destDir, "SKILL.md")); err == nil {
				continue
			}
		}

		// Copy the entire skill directory from the embed.
		srcDir := filepath.Join("agent/skills", skillID)
		if err := fs.WalkDir(resources.BuiltinSkillsFS, srcDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(srcDir, path)
			if err != nil {
				return err
			}
			destPath := filepath.Join(destDir, rel)
			if d.IsDir() {
				return os.MkdirAll(destPath, 0o755)
			}
			data, err := resources.BuiltinSkillsFS.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(destPath, data, 0o644)
		}); err != nil {
			continue // skip broken skill, don't abort whole seed
		}

		// Record provenance.
		provenance[skillID] = provenanceEntry{
			CreatedBy: "builtin",
			CreatedAt: clock.NewTime().RFC3339(),
		}
	}

	// Cleanup orphan builtin skills (exist in dest but not in source).
	entries, err = os.ReadDir(root)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !skillIDRe.MatchString(entry.Name()) {
				continue
			}
			orphanID := entry.Name()
			if sourceIDs[orphanID] {
				continue
			}
			if prov, ok := provenance[orphanID]; ok && prov.CreatedBy == "builtin" {
				_ = os.RemoveAll(filepath.Join(root, orphanID))
				delete(provenance, orphanID)
			}
		}
	}

	// Persist provenance.
	if err := saveProvenance(root, provenance); err != nil {
		return err
	}

	// Clean stale deletion records for skills no longer in source.
	changed := false
	for id := range deleted {
		if !sourceIDs[id] {
			delete(deleted, id)
			changed = true
		}
	}
	if changed {
		if err := saveDeletedBuiltin(root, deleted); err != nil {
			return err
		}
	}

	return nil
}

// List returns all skills from the filesystem (user/builtin + mounted
// plugin skills), sorted by name.
func (s *Store) List() []*domain.Skill {
	var out []*domain.Skill

	// Scan root directory (user + builtin skills).
	entries, err := os.ReadDir(s.root)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !skillIDRe.MatchString(entry.Name()) {
				continue
			}
			skill, err := s.loadSkillFromDir(entry.Name(), s.root)
			if err != nil {
				continue
			}
			out = append(out, skill)
		}
	}

	// Scan plugin mounts.
	s.mu.RLock()
	mounts := make(map[string]string, len(s.pluginMounts))
	for k, v := range s.pluginMounts {
		mounts[k] = v
	}
	s.mu.RUnlock()
	for owner, dir := range mounts {
		pluginEntries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range pluginEntries {
			if !entry.IsDir() || !skillIDRe.MatchString(entry.Name()) {
				continue
			}
			skill, err := s.loadSkillFromDir(entry.Name(), dir)
			if err != nil {
				continue
			}
			skill.SetOwner(owner, dir)
			out = append(out, skill)
		}
	}

	// Sort by name.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Name < out[i].Name {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Get returns a skill by ID. If ownedBy is empty, priority resolution
// picks user > builtin > plugin. If ownedBy is set, returns the exact
// skill with that owner.
func (s *Store) Get(id, ownedBy string) (*domain.Skill, error) {
	if !skillIDRe.MatchString(id) {
		return nil, fmt.Errorf("skill %q not found", id)
	}

	// Exact owner lookup.
	if ownedBy != "" {
		return s.getWithOwner(id, ownedBy)
	}

	// Priority resolution: collect all skills with this ID.
	candidates := s.skillsByID(id)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	// Sort by priority (user > builtin > plugin).
	sort.Slice(candidates, func(i, j int) bool {
		return domain.SkillOwnerPriority(candidates[i].EffectiveOwnedBy()) < domain.SkillOwnerPriority(candidates[j].EffectiveOwnedBy())
	})
	return candidates[0], nil
}

// getWithOwner returns the skill with the exact owner.
func (s *Store) getWithOwner(id, ownedBy string) (*domain.Skill, error) {
	// Root-resident owners: user, builtin, and agent-authored skills all
	// live directly under s.root (skill op=save persists agent-origin skills
	// there), so an exact-owner lookup for them resolves against the root,
	// NOT against a plugin mount.
	if ownedBy == "user" || ownedBy == "builtin" ||
		ownedBy == string(domain.SkillOriginUser) ||
		ownedBy == string(domain.SkillOriginBuiltin) ||
		ownedBy == string(domain.SkillOriginAgent) {
		skill, err := s.loadSkillFromDir(id, s.root)
		if err != nil {
			return nil, fmt.Errorf("skill %q not found", id)
		}
		return skill, nil
	}
	// Plugin: read from mount.
	s.mu.RLock()
	dir, ok := s.pluginMounts[ownedBy]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("skill %q not found (owner %s not mounted)", id, ownedBy)
	}
	skill, err := s.loadSkillFromDir(id, dir)
	if err != nil {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	skill.SetOwner(ownedBy, dir)
	return skill, nil
}

// skillsByID returns all skills (across root + plugin mounts) with the
// given ID.
func (s *Store) skillsByID(id string) []*domain.Skill {
	var out []*domain.Skill
	// Check root.
	if skill, err := s.loadSkillFromDir(id, s.root); err == nil {
		out = append(out, skill)
	}
	// Check plugin mounts.
	s.mu.RLock()
	mounts := make(map[string]string, len(s.pluginMounts))
	for k, v := range s.pluginMounts {
		mounts[k] = v
	}
	s.mu.RUnlock()
	for owner, dir := range mounts {
		if skill, err := s.loadSkillFromDir(id, dir); err == nil {
			skill.SetOwner(owner, dir)
			out = append(out, skill)
		}
	}
	return out
}

// Save writes a skill's SKILL.md to the filesystem and updates metadata
// in skills.json. The skill ID is the folder name, which must match the
// skill name (lowercase with hyphens, matching the SKILL_ID pattern).
// If the skill has no ID, the name is used as the ID. If an existing
// skill is renamed, the old folder is removed.
func (s *Store) Save(skill *domain.Skill) error {
	// Plugin-owned skills are read-only.
	if strings.HasPrefix(skill.EffectiveOwnedBy(), "plugin:") {
		return fmt.Errorf("plugin-owned skills are read-only; uninstall the plugin to modify")
	}
	// Use the skill name as the folder ID (matching Electron's pattern).
	if skill.ID == "" {
		skill.ID = skill.Name
	}
	if !skillIDRe.MatchString(skill.ID) {
		return fmt.Errorf("skillfs: invalid skill id %q (must be lowercase with hyphens)", skill.ID)
	}
	dir := filepath.Join(s.root, skill.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("skillfs: mkdir %s: %w", dir, err)
	}
	// Write SKILL.md with frontmatter.
	content := formatSkillMarkdown(skill)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("skillfs: write SKILL.md: %w", err)
	}
	// Update metadata.
	skill.Touch(clock.NewTime().Time())
	s.json.set(skill)
	if err := s.json.save(); err != nil {
		return err
	}
	// Update provenance for new skills.
	prov, _ := loadProvenance(s.root)
	if _, exists := prov[skill.ID]; !exists {
		origin := string(skill.Origin)
		if origin == "" {
			origin = "user"
		}
		prov[skill.ID] = provenanceEntry{
			CreatedBy: origin,
			CreatedAt: clock.NewTime().RFC3339(),
		}
		_ = saveProvenance(s.root, prov)
	}
	return nil
}

// Delete removes a skill directory and its metadata. If the skill is
// builtin, it is recorded in .deleted-builtin.json so it is not
// re-seeded on restart. Plugin-owned skills cannot be deleted directly.
func (s *Store) Delete(id, ownedBy string) error {
	if !skillIDRe.MatchString(id) {
		return fmt.Errorf("skill %q not found", id)
	}
	// Resolve the skill to check its owner.
	skill, err := s.Get(id, ownedBy)
	if err != nil {
		return err
	}
	owner := skill.EffectiveOwnedBy()
	if strings.HasPrefix(owner, "plugin:") {
		return fmt.Errorf("skill %q is owned by plugin %s; uninstall the plugin to remove it", id, strings.TrimPrefix(owner, "plugin:"))
	}
	dir := filepath.Join(s.root, id)
	// Check provenance before deleting.
	prov, _ := loadProvenance(s.root)
	if entry, ok := prov[id]; ok && entry.CreatedBy == "builtin" {
		deleted, _ := loadDeletedBuiltin(s.root)
		deleted[id] = deletedEntry{DeletedAt: clock.NewTime().RFC3339()}
		_ = saveDeletedBuiltin(s.root, deleted)
	}
	// Remove the directory.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("skillfs: remove %s: %w", dir, err)
	}
	// Remove from metadata + provenance.
	s.json.delete(metaKey(id, owner))
	if err := s.json.save(); err != nil {
		return err
	}
	delete(prov, id)
	_ = saveProvenance(s.root, prov)
	return nil
}

// loadSkillFromDir reads SKILL.md from <dir>/<id>/SKILL.md and merges
// metadata from skills.json. The dir parameter is the parent directory
// (either s.root for user/builtin skills, or a plugin mount directory).
func (s *Store) loadSkillFromDir(id, dir string) (*domain.Skill, error) {
	skillFile := filepath.Join(dir, id, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		return nil, fmt.Errorf("skillfs: read %s: %w", skillFile, err)
	}
	name, desc, content := parseSkillMarkdown(string(data))
	skill := &domain.Skill{
		ID:          id,
		Name:        name,
		Description: desc,
		Content:     content,
	}
	// Merge metadata from skills.json. Try composite key first (for
	// plugin skills), then flat key (for user/builtin), then the
	// "agent:<id>" composite that Save writes for agent-origin skills
	// (their OwnedBy is empty during load, so the first probe misses).
	applyMeta := func(meta *skillMeta) {
		skill.Category = meta.Category
		skill.State = meta.State
		skill.Origin = meta.Origin
		skill.OwnedBy = meta.OwnedBy
		skill.PluginDir = meta.PluginDir
		skill.Pinned = meta.Pinned
		skill.UsageCount = meta.UsageCount
		skill.LastUsedAt = meta.LastUsedAt
		skill.UpdatedAt = meta.UpdatedAt
	}
	if meta, ok := s.json.get(metaKey(id, skill.OwnedBy)); ok {
		applyMeta(meta)
	} else if meta, ok := s.json.get(id); ok {
		applyMeta(meta)
	} else if meta, ok := s.json.get(string(domain.SkillOriginAgent) + ":" + id); ok {
		applyMeta(meta)
	}
	// If no metadata, set defaults from provenance.
	skill.EnsureStateDefault()
	if skill.Origin == "" {
		prov, _ := loadProvenance(s.root)
		if entry, ok := prov[id]; ok {
			skill.Origin = domain.SkillOrigin(entry.CreatedBy)
		} else {
			skill.Origin = domain.SkillOriginUser
		}
	}
	return skill, nil
}

// --- SKILL.md parsing / formatting ---

// parseSkillMarkdown extracts frontmatter (name, description) and the
// body content from a SKILL.md file. If there is no frontmatter, the
// entire content is returned as the body and name is the filename.
func parseSkillMarkdown(raw string) (name, description, content string) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return "", "", raw
	}
	// Find closing ---.
	rest := raw[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", "", raw
	}
	frontmatter := strings.TrimSpace(rest[:idx])
	body := strings.TrimSpace(rest[idx+4:])
	// Parse simple YAML-like frontmatter.
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.Trim(name, "\"'")
		}
		if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			description = strings.Trim(description, "\"'")
		}
	}
	return name, description, body
}

// formatSkillMarkdown produces a SKILL.md with frontmatter + body.
func formatSkillMarkdown(s *domain.Skill) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: ")
	sb.WriteString(s.Name)
	sb.WriteString("\n")
	if s.Description != "" {
		sb.WriteString("description: ")
		sb.WriteString(s.Description)
		sb.WriteString("\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString(s.Content)
	if !strings.HasSuffix(s.Content, "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

// --- Metadata store (skills.json) ---

type skillMeta struct {
	Category   string             `json:"category,omitempty"`
	State      domain.SkillState  `json:"state,omitempty"`
	Origin     domain.SkillOrigin `json:"origin,omitempty"`
	OwnedBy    string             `json:"owned_by,omitempty"`
	PluginDir  string             `json:"plugin_dir,omitempty"` // mount source for plugin skills
	Pinned     bool               `json:"pinned,omitempty"`
	UsageCount int                `json:"usage_count,omitempty"`
	LastUsedAt time.Time          `json:"last_used_at,omitempty"`
	UpdatedAt  time.Time          `json:"updated_at,omitempty"`
}

type jsonMetaStore struct {
	path  string
	items map[string]*skillMeta
}

func (j *jsonMetaStore) get(id string) (*skillMeta, bool) {
	m, ok := j.items[id]
	return m, ok
}

func (j *jsonMetaStore) set(s *domain.Skill) {
	key := metaKey(s.ID, s.EffectiveOwnedBy())
	j.items[key] = &skillMeta{
		Category:   s.Category,
		State:      s.State,
		Origin:     s.Origin,
		OwnedBy:    s.OwnedBy,
		PluginDir:  s.PluginDir,
		Pinned:     s.Pinned,
		UsageCount: s.UsageCount,
		LastUsedAt: s.LastUsedAt,
		UpdatedAt:  s.UpdatedAt,
	}
}

// metaKey returns the composite key for the metadata store. User/builtin
// skills use their ID directly (backward compatible). Plugin skills use
// "plugin:<pluginID>:<skillID>".
func metaKey(id, ownedBy string) string {
	if ownedBy == "" || ownedBy == "user" || ownedBy == "builtin" || ownedBy == string(domain.SkillOriginUser) || ownedBy == string(domain.SkillOriginBuiltin) {
		return id
	}
	return ownedBy + ":" + id
}

func (j *jsonMetaStore) delete(id string) {
	delete(j.items, id)
}

func (j *jsonMetaStore) save() error {
	data, err := json.MarshalIndent(j.items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(j.path, data, 0o644)
}

// --- Provenance / deleted-builtin sidecars ---

type provenanceEntry struct {
	CreatedBy string `json:"createdBy"`
	CreatedAt string `json:"createdAt"`
}

type deletedEntry struct {
	DeletedAt string `json:"deletedAt"`
}

func loadProvenance(root string) (map[string]provenanceEntry, error) {
	path := filepath.Join(root, ".provenance.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]provenanceEntry), nil
	}
	var out map[string]provenanceEntry
	if err := json.Unmarshal(data, &out); err != nil {
		return make(map[string]provenanceEntry), nil
	}
	if out == nil {
		out = make(map[string]provenanceEntry)
	}
	return out, nil
}

func saveProvenance(root string, prov map[string]provenanceEntry) error {
	path := filepath.Join(root, ".provenance.json")
	data, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadDeletedBuiltin(root string) (map[string]deletedEntry, error) {
	path := filepath.Join(root, ".deleted-builtin.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]deletedEntry), nil
	}
	var out map[string]deletedEntry
	if err := json.Unmarshal(data, &out); err != nil {
		return make(map[string]deletedEntry), nil
	}
	if out == nil {
		out = make(map[string]deletedEntry)
	}
	return out, nil
}

func saveDeletedBuiltin(root string, deleted map[string]deletedEntry) error {
	path := filepath.Join(root, ".deleted-builtin.json")
	data, err := json.MarshalIndent(deleted, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Compile-time interface check.
var _ application.SkillStore = (*Store)(nil)

// --- enriched skill file reads (port of Electron skill read) ---

const maxSkillEditableBytes = 1024 * 1024

// Uncompressed-size caps for installing a skill from a ZIP archive.
// Without them a small malicious archive (zip bomb) could expand to
// gigabytes and fill the disk.
const (
	maxSkillZipFileBytes    = 32 * 1024 * 1024
	maxSkillZipArchiveBytes = 128 * 1024 * 1024
)

// normalizedRel validates a skill-relative path: posix separators, no
// leading "/", no empty/".." segments. Returns the cleaned path.
func normalizedRel(path string) (string, error) {
	p := strings.ReplaceAll(path, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	if p == "" || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("invalid skill path")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == ".." {
			return "", fmt.Errorf("invalid skill path")
		}
	}
	return p, nil
}

// safeSkillPath resolves a skill-relative path to an absolute path inside
// the skill's directory, refusing traversal.
func (s *Store) safeSkillPath(id, ownedBy, rel string) (string, error) {
	if !skillIDRe.MatchString(id) {
		return "", fmt.Errorf("skill %q not found", id)
	}
	clean, err := normalizedRel(rel)
	if err != nil {
		return "", fmt.Errorf("skill %q: %w", id, err)
	}
	// Resolve the skill directory based on ownedBy.
	var rootDir string
	if ownedBy != "" && strings.HasPrefix(ownedBy, "plugin:") {
		s.mu.RLock()
		dir, ok := s.pluginMounts[ownedBy]
		s.mu.RUnlock()
		if !ok {
			return "", fmt.Errorf("skill %q not found (owner %s not mounted)", id, ownedBy)
		}
		rootDir = filepath.Join(dir, id)
	} else {
		rootDir = filepath.Join(s.root, id)
	}
	if !strings.HasPrefix(rootDir, s.root+string(filepath.Separator)) && rootDir != s.root && !strings.HasPrefix(rootDir, s.root) {
		// Plugin dirs are outside root — that's expected. Just ensure
		// the resolved target stays within rootDir.
	}
	target := filepath.Join(rootDir, filepath.FromSlash(clean))
	if target != rootDir && !strings.HasPrefix(target, rootDir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid skill path")
	}
	info, err := os.Stat(target)
	if err == nil && info.IsDir() {
		return "", fmt.Errorf("skill file not found: %s", rel)
	}
	return target, nil
}

func isUTF8Text(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return utf8.Valid(data)
}

// ReadFile implements SkillStore.ReadFile.
func (s *Store) ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error) {
	rel := strings.TrimSpace(path)
	if rel == "" {
		rel = "SKILL.md"
	}
	target, err := s.safeSkillPath(id, ownedBy, rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, fmt.Errorf("skill file not found: %s", rel)
	}
	editable := len(data) <= maxSkillEditableBytes && isUTF8Text(data)
	sf := &domain.SkillFile{
		SkillID:   id,
		Path:      strings.ReplaceAll(rel, "\\", "/"),
		SizeBytes: int64(len(data)),
		Editable:  editable,
	}
	if !editable {
		return sf, nil
	}
	text := string(data)
	start := offset
	if start < 0 {
		start = 0
	}
	if start > len(text) {
		start = len(text)
	}
	limit := maxChars
	if limit <= 0 {
		limit = 20000
	}
	if limit > 100000 {
		limit = 100000
	}
	if start+limit > len(text) {
		limit = len(text) - start
	}
	sf.Content = text[start : start+limit]
	if start+limit < len(text) {
		sf.Truncated = true
		sf.NextOffset = start + limit
	}
	return sf, nil
}

// WriteFile writes content to a file inside a skill directory (default
// SKILL.md). Parent directories (e.g. references/, templates/, scripts/)
// are created as needed. The skill must already exist; plugin-owned skills
// are read-only. Path traversal is rejected via safeSkillPath.
func (s *Store) WriteFile(id, ownedBy, path, content string) error {
	skill, err := s.Get(id, ownedBy)
	if err != nil {
		return fmt.Errorf("skill %q not found: %w", id, err)
	}
	if strings.HasPrefix(skill.EffectiveOwnedBy(), "plugin:") {
		return fmt.Errorf("plugin-owned skills are read-only; uninstall the plugin to modify")
	}
	rel := strings.TrimSpace(path)
	if rel == "" {
		rel = "SKILL.md"
	}
	target, err := s.safeSkillPath(id, ownedBy, rel)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(target); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("skillfs: mkdir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return fmt.Errorf("skillfs: write %s: %w", rel, err)
	}
	// Touch skill metadata so UpdatedAt reflects the change.
	skill.Touch(clock.NewTime().Time())
	s.json.set(skill)
	_ = s.json.save()
	return nil
}

// Files implements SkillStore.Files and lists the skill directory tree.
func (s *Store) Files(id, ownedBy string) ([]domain.SkillFileEntry, error) {
	if !skillIDRe.MatchString(id) {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	var rootDir string
	if ownedBy != "" && strings.HasPrefix(ownedBy, "plugin:") {
		s.mu.RLock()
		dir, ok := s.pluginMounts[ownedBy]
		s.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("skill %q not found (owner %s not mounted)", id, ownedBy)
		}
		rootDir = filepath.Join(dir, id)
	} else {
		rootDir = filepath.Join(s.root, id)
	}
	var out []domain.SkillFileEntry
	var walkDir func(dir, prefix string) error
	walkDir = func(dir, prefix string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			relPath := name
			if prefix != "" {
				relPath = prefix + "/" + name
			}
			full := filepath.Join(dir, name)
			if entry.IsDir() {
				out = append(out, domain.SkillFileEntry{Path: relPath, Type: "directory"})
				if err := walkDir(full, relPath); err != nil {
					return err
				}
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			editable := false
			if info.Size() <= maxSkillEditableBytes {
				if data, err := os.ReadFile(full); err == nil {
					editable = isUTF8Text(data)
				}
			}
			out = append(out, domain.SkillFileEntry{Path: relPath, Type: "file", SizeBytes: info.Size(), Editable: editable})
		}
		return nil
	}
	if err := walkDir(rootDir, ""); err != nil {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	return out, nil
}

// Install extracts a .skill (zip) archive into the skill root directory.
// The archive must contain a top-level directory with a SKILL.md file.
// The skill ID is derived from the top-level directory name. If a skill
// with the same ID already exists, it is overwritten.
func (s *Store) Install(zipData []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("skillfs: invalid zip archive: %w", err)
	}

	// Find the top-level directory name and verify SKILL.md exists.
	var topLevel string
	hasSkillMD := false
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(f.Name, "/"), "/", 2)
		if topLevel == "" {
			topLevel = parts[0]
		} else if parts[0] != topLevel {
			return "", fmt.Errorf("skillfs: archive has multiple top-level directories")
		}
		if strings.HasSuffix(f.Name, "/SKILL.md") || f.Name == topLevel+"/SKILL.md" {
			hasSkillMD = true
		}
	}
	if topLevel == "" {
		return "", fmt.Errorf("skillfs: archive is empty")
	}
	if !hasSkillMD {
		return "", fmt.Errorf("skillfs: archive missing SKILL.md in %s/", topLevel)
	}
	if !skillIDRe.MatchString(topLevel) {
		return "", fmt.Errorf("skillfs: invalid skill id %q (must be lowercase with hyphens)", topLevel)
	}

	// Extract into skill root, overwriting any existing skill with same ID.
	destDir := filepath.Join(s.root, topLevel)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("skillfs: mkdir %s: %w", destDir, err)
	}
	var totalUncompressed int64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// Path relative to the top-level directory. Zip entries always
		// use forward slashes; convert to the native path separator so
		// filepath.Join and os.MkdirAll work correctly on Windows.
		rel := strings.TrimPrefix(f.Name, topLevel+"/")
		if rel == f.Name {
			// File at root without top-level prefix — skip (shouldn't happen).
			continue
		}
		rel = filepath.FromSlash(rel)
		dest := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", fmt.Errorf("skillfs: mkdir for %s: %w", dest, err)
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("skillfs: open %s: %w", f.Name, err)
		}
		var buf bytes.Buffer
		n, err := io.Copy(&buf, io.LimitReader(rc, maxSkillZipFileBytes+1))
		rc.Close()
		if err != nil {
			return "", fmt.Errorf("skillfs: read %s: %w", f.Name, err)
		}
		if n > maxSkillZipFileBytes {
			return "", fmt.Errorf("skillfs: file %s expands past %d byte limit (zip bomb?)", f.Name, maxSkillZipFileBytes)
		}
		totalUncompressed += n
		if totalUncompressed > maxSkillZipArchiveBytes {
			return "", fmt.Errorf("skillfs: archive expands past %d byte total limit (zip bomb?)", maxSkillZipArchiveBytes)
		}
		if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
			return "", fmt.Errorf("skillfs: write %s: %w", dest, err)
		}
	}

	// Parse SKILL.md frontmatter to get name + description.
	skillMD, err := os.ReadFile(filepath.Join(destDir, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("skillfs: read SKILL.md: %w", err)
	}
	name, description := parseFrontmatter(string(skillMD))

	// Register metadata.
	skill := &domain.Skill{
		ID:          topLevel,
		Name:        name,
		Description: description,
		State:       domain.SkillStateActive,
		Origin:      domain.SkillOriginUser,
		OwnedBy:     "user",
		UpdatedAt:   clock.NewTime().Time(),
	}
	s.json.set(skill)
	if err := s.json.save(); err != nil {
		return "", err
	}

	// Update provenance.
	prov, _ := loadProvenance(s.root)
	prov[topLevel] = provenanceEntry{
		CreatedBy: "user",
		CreatedAt: clock.NewTime().RFC3339(),
	}
	_ = saveProvenance(s.root, prov)

	return topLevel, nil
}

// parseFrontmatter extracts name and description from YAML frontmatter
// in a SKILL.md file. Returns ("", "") if no frontmatter is found.
func parseFrontmatter(content string) (name, description string) {
	if !strings.HasPrefix(content, "---") {
		return "", ""
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return "", ""
	}
	frontmatter := content[3 : end+3]
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.Trim(name, `"'`)
		}
		if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			description = strings.Trim(description, `"'`)
		}
	}
	if name == "" {
		name = "unnamed"
	}
	return name, description
}

// MountPluginSkills scans a plugin's skills/ directory and registers all
// skill packages found there with owned_by="plugin:<pluginID>". File
// content is read from the plugin directory (mount, no copy).
func (s *Store) MountPluginSkills(pluginID, pluginSkillsDir string) error {
	if pluginID == "" {
		return fmt.Errorf("skillfs: plugin ID is required for mount")
	}
	owner := "plugin:" + pluginID
	// Check if the directory exists.
	info, err := os.Stat(pluginSkillsDir)
	if err != nil || !info.IsDir() {
		// No skills/ directory — nothing to mount, not an error.
		return nil
	}
	// Register the mount.
	s.mu.Lock()
	s.pluginMounts[owner] = pluginSkillsDir
	s.mu.Unlock()
	// Scan for skill directories and register metadata.
	entries, err := os.ReadDir(pluginSkillsDir)
	if err != nil {
		return fmt.Errorf("skillfs: scan plugin skills %s: %w", pluginSkillsDir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !skillIDRe.MatchString(entry.Name()) {
			continue
		}
		skillID := entry.Name()
		// Load skill to get name/description from SKILL.md.
		skill, err := s.loadSkillFromDir(skillID, pluginSkillsDir)
		if err != nil {
			continue
		}
		skill.SetOwner(owner, pluginSkillsDir)
		skill.Origin = domain.SkillOriginUser // placeholder; OwnedBy is authoritative
		skill.Touch(clock.NewTime().Time())
		s.json.set(skill)
	}
	return s.json.save()
}

// UnmountPluginSkills removes all skills owned by plugin:<pluginID>
// from the metadata catalog. Files in the plugin directory are not
// touched (the plugin uninstaller handles those).
func (s *Store) UnmountPluginSkills(pluginID string) error {
	if pluginID == "" {
		return fmt.Errorf("skillfs: plugin ID is required for unmount")
	}
	owner := "plugin:" + pluginID
	s.mu.Lock()
	delete(s.pluginMounts, owner)
	s.mu.Unlock()
	// Remove all metadata entries for this owner.
	for key, meta := range s.json.items {
		if meta.OwnedBy == owner {
			delete(s.json.items, key)
		}
	}
	// Also clean composite keys (plugin:<pluginID>:<skillID>).
	prefix := owner + ":"
	for key := range s.json.items {
		if strings.HasPrefix(key, prefix) {
			delete(s.json.items, key)
		}
	}
	return s.json.save()
}
