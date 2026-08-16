// Package skillfs implements a filesystem-backed skill store that mirrors
// NusaShell Electron's skill architecture:
//
//   - Builtin skills are embedded in the binary (resources/agent/skills/)
//     and seeded into the user data directory on startup.
//   - Skill content (SKILL.md + support files) lives on the filesystem
//     under <datadir>/agent/skills/<name>/.
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
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/resources"
)

var skillIDRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Store implements application.SkillStore backed by the filesystem.
// Skill content (SKILL.md) is read from <root>/<name>/SKILL.md. Metadata
// (state, origin, usage, pinned) is persisted in skills.json via the
// embedded jsonstore. Content is never stored in skills.json — only
// cataloged metadata.
type Store struct {
	root string // <datadir>/agent/skills
	json *jsonMetaStore
}

// New creates a filesystem skill store rooted at root. The directory is
// created if it does not exist.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("skillfs: mkdir %s: %w", root, err)
	}
	j, err := loadMetaStore(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: root, json: j}, nil
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
		if err := copyEmbedDir(filepath.Join("agent/skills", skillID), destDir); err != nil {
			continue // skip broken skill, don't abort whole seed
		}

		// Record provenance.
		provenance[skillID] = provenanceEntry{
			CreatedBy: "builtin",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}

	// Cleanup orphan builtin skills (exist in dest but not in source).
	cleanupOrphanBuiltin(root, sourceIDs, provenance, deleted)

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

// List returns all skills from the filesystem, sorted by name.
func (s *Store) List() []*domain.Skill {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	var out []*domain.Skill
	for _, entry := range entries {
		if !entry.IsDir() || !skillIDRe.MatchString(entry.Name()) {
			continue
		}
		skill, err := s.loadSkill(entry.Name())
		if err != nil {
			continue
		}
		out = append(out, skill)
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

// Get returns a skill by ID (which is the folder name).
func (s *Store) Get(id string) (*domain.Skill, error) {
	if !skillIDRe.MatchString(id) {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	return s.loadSkill(id)
}

// Save writes a skill's SKILL.md to the filesystem and updates metadata
// in skills.json. The skill ID is the folder name, which must match the
// skill name (lowercase with hyphens, matching the SKILL_ID pattern).
// If the skill has no ID, the name is used as the ID. If an existing
// skill is renamed, the old folder is removed.
func (s *Store) Save(skill *domain.Skill) error {
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
	skill.UpdatedAt = time.Now().UTC()
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
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		_ = saveProvenance(s.root, prov)
	}
	return nil
}

// Delete removes a skill directory and its metadata. If the skill is
// builtin, it is recorded in .deleted-builtin.json so it is not
// re-seeded on restart.
func (s *Store) Delete(id string) error {
	if !skillIDRe.MatchString(id) {
		return fmt.Errorf("skill %q not found", id)
	}
	dir := filepath.Join(s.root, id)
	// Check provenance before deleting.
	prov, _ := loadProvenance(s.root)
	if entry, ok := prov[id]; ok && entry.CreatedBy == "builtin" {
		deleted, _ := loadDeletedBuiltin(s.root)
		deleted[id] = deletedEntry{DeletedAt: time.Now().UTC().Format(time.RFC3339)}
		_ = saveDeletedBuiltin(s.root, deleted)
	}
	// Remove the directory.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("skillfs: remove %s: %w", dir, err)
	}
	// Remove from metadata + provenance.
	s.json.delete(id)
	if err := s.json.save(); err != nil {
		return err
	}
	delete(prov, id)
	_ = saveProvenance(s.root, prov)
	return nil
}

// loadSkill reads SKILL.md from <root>/<id>/SKILL.md and merges metadata
// from skills.json.
func (s *Store) loadSkill(id string) (*domain.Skill, error) {
	skillFile := filepath.Join(s.root, id, "SKILL.md")
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
	// Merge metadata from skills.json.
	if meta, ok := s.json.get(id); ok {
		skill.Category = meta.Category
		skill.State = meta.State
		skill.Origin = meta.Origin
		skill.Pinned = meta.Pinned
		skill.UsageCount = meta.UsageCount
		skill.LastUsedAt = meta.LastUsedAt
		skill.UpdatedAt = meta.UpdatedAt
	}
	// If no metadata, set defaults from provenance.
	if skill.State == "" {
		skill.State = domain.SkillStateActive
	}
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
	Pinned     bool               `json:"pinned,omitempty"`
	UsageCount int                `json:"usage_count,omitempty"`
	LastUsedAt time.Time          `json:"last_used_at,omitempty"`
	UpdatedAt  time.Time          `json:"updated_at,omitempty"`
}

type jsonMetaStore struct {
	path  string
	items map[string]*skillMeta
}

func loadMetaStore(root string) (*jsonMetaStore, error) {
	store := &jsonMetaStore{
		path:  filepath.Join(root, "skills.json"),
		items: make(map[string]*skillMeta),
	}
	data, err := os.ReadFile(store.path)
	if err == nil {
		_ = json.Unmarshal(data, &store.items)
	}
	return store, nil
}

func (j *jsonMetaStore) get(id string) (*skillMeta, bool) {
	m, ok := j.items[id]
	return m, ok
}

func (j *jsonMetaStore) set(s *domain.Skill) {
	j.items[s.ID] = &skillMeta{
		Category:   s.Category,
		State:      s.State,
		Origin:     s.Origin,
		Pinned:     s.Pinned,
		UsageCount: s.UsageCount,
		LastUsedAt: s.LastUsedAt,
		UpdatedAt:  s.UpdatedAt,
	}
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

// --- Embed copy helpers ---

func copyEmbedDir(src, dest string) error {
	return fs.WalkDir(resources.BuiltinSkillsFS, src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}
		data, err := resources.BuiltinSkillsFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0o644)
	})
}

func cleanupOrphanBuiltin(root string, sourceIDs map[string]bool, provenance map[string]provenanceEntry, deleted map[string]deletedEntry) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !skillIDRe.MatchString(entry.Name()) {
			continue
		}
		skillID := entry.Name()
		if sourceIDs[skillID] {
			continue
		}
		if prov, ok := provenance[skillID]; ok && prov.CreatedBy == "builtin" {
			_ = os.RemoveAll(filepath.Join(root, skillID))
			delete(provenance, skillID)
		}
	}
}

// Compile-time interface check.
var _ application.SkillStore = (*Store)(nil)

// --- enriched skill file reads (port of Electron skill_read) ---

const maxSkillEditableBytes = 1024 * 1024

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
func (s *Store) safeSkillPath(id, rel string) (string, error) {
	if !skillIDRe.MatchString(id) {
		return "", fmt.Errorf("skill %q not found", id)
	}
	clean, err := normalizedRel(rel)
	if err != nil {
		return "", fmt.Errorf("skill %q: %w", id, err)
	}
	rootDir := filepath.Join(s.root, id)
	if !strings.HasPrefix(rootDir, s.root+string(filepath.Separator)) && rootDir != s.root {
		return "", fmt.Errorf("invalid skill path")
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
func (s *Store) ReadFile(id, path string, offset, maxChars int) (*domain.SkillFile, error) {
	rel := strings.TrimSpace(path)
	if rel == "" {
		rel = "SKILL.md"
	}
	target, err := s.safeSkillPath(id, rel)
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

// Files implements SkillStore.Files and lists the skill directory tree.
func (s *Store) Files(id string) ([]domain.SkillFileEntry, error) {
	if !skillIDRe.MatchString(id) {
		return nil, fmt.Errorf("skill %q not found", id)
	}
	rootDir := filepath.Join(s.root, id)
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
