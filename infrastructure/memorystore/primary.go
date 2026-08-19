// Package memorystore implements the two-tier memory persistence
// adapters: a single MEMORY.md file for the always-injected primary
// working set, and one markdown file per entry under memories/fragments/
// for the unlimited searchable archive. Both adapters auto-create their
// files and directories on first use.
package memorystore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"nusashell/domain"
)

// ---- Primary store (MEMORY.md) ----

// PrimaryFile is the on-disk name of the primary memory document.
const PrimaryFile = "MEMORY.md"

// PrimaryVersion is the schema version of the MEMORY.md frontmatter.
// Bump when the file format changes in a backward-incompatible way.
const PrimaryVersion = 1

// primaryFrontmatter is the YAML metadata block at the top of MEMORY.md.
// It carries the last-updated timestamp and schema version so a human
// (or migration tool) can tell when the file was last touched and what
// format version it uses.
type primaryFrontmatter struct {
	LastUpdated string `yaml:"last_updated"`
	Version     int    `yaml:"version"`
}

// Primary is the PrimaryStore adapter backed by MEMORY.md. The file is
// a markdown document with YAML frontmatter (last_updated + version)
// followed by one bullet line per entry. Each entry line is formatted as:
//
//   - [id] content
//
// so it stays readable when opened by hand and parseable by the adapter.
type Primary struct {
	mu      sync.RWMutex
	path    string
	entries []domain.PrimaryEntry
	loaded  bool
}

// NewPrimary opens (or auto-creates) the MEMORY.md file at dataDir and
// loads its entries into memory. The file is created empty with YAML
// frontmatter if it does not exist.
func NewPrimary(dataDir string) (*Primary, error) {
	p := &Primary{path: filepath.Join(dataDir, PrimaryFile)}
	if err := p.load(true); err != nil {
		return nil, err
	}
	return p, nil
}

// load reads the file from disk, creating it empty if create is true
// and the file is missing. Safe to call repeatedly; subsequent calls
// re-read the file so updates from other processes are picked up.
func (p *Primary) load(create bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, err := os.ReadFile(p.path)
	if err != nil {
		if !create || !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p.path, []byte(emptyPrimaryFile()), 0o644); err != nil {
			return err
		}
		p.entries = nil
		p.loaded = true
		return nil
	}
	entries, err := parsePrimary(string(raw))
	if err != nil {
		return err
	}
	p.entries = entries
	p.loaded = true
	return nil
}

// Load returns the current primary memory document. It re-reads the
// file from disk so callers always see the latest state.
func (p *Primary) Load() *domain.PrimaryMemory {
	_ = p.load(false)
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]domain.PrimaryEntry, len(p.entries))
	copy(out, p.entries)
	updated := time.Time{}
	for _, e := range out {
		if e.UpdatedAt.After(updated) {
			updated = e.UpdatedAt
		}
	}
	return &domain.PrimaryMemory{Entries: out, UpdatedAt: updated}
}

// Update replaces the entire primary entry list and rewrites the file.
// Used by memory_promote / memory_demote. Returns an error if the new
// content would exceed PrimaryCharCap.
func (p *Primary) Update(entries []domain.PrimaryEntry) error {
	total := 0
	for _, e := range entries {
		total += len(e.Content)
	}
	if total > domain.PrimaryCharCap {
		return fmt.Errorf("primary memory at %d/%d chars; promote/demote to fit — primary is capped at ~%d tokens", total, domain.PrimaryCharCap, domain.PrimaryTokenCap)
	}
	p.mu.Lock()
	p.entries = entries
	err := p.writeFile()
	p.mu.Unlock()
	return err
}

// Replace performs a substring-match update on a single entry. The
// first entry whose content contains oldText is updated to content.
// Used by foreground agents via memory_replace.
func (p *Primary) Replace(oldText, content string) error {
	oldText = strings.TrimSpace(oldText)
	content = strings.TrimSpace(content)
	if oldText == "" || content == "" {
		return fmt.Errorf("oldText and content are required")
	}
	_ = p.load(false)
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := -1
	for i, e := range p.entries {
		if strings.Contains(e.Content, oldText) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no primary entry matching %q", oldText)
	}
	// Recompute total with the new content to enforce the cap.
	total := 0
	for i, e := range p.entries {
		if i == idx {
			total += len(content)
		} else {
			total += len(e.Content)
		}
	}
	if total > domain.PrimaryCharCap {
		return fmt.Errorf("primary memory at %d/%d chars; update would exceed the ~%d token cap", total, domain.PrimaryCharCap, domain.PrimaryTokenCap)
	}
	p.entries[idx].Content = content
	p.entries[idx].UpdatedAt = time.Now().UTC()
	return p.writeFile()
}

// writeFile serializes the current entries to the markdown file with
// YAML frontmatter (last_updated + version). Caller must hold p.mu.
func (p *Primary) writeFile() error {
	fm := primaryFrontmatter{
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
		Version:     PrimaryVersion,
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	for _, e := range p.entries {
		b.WriteString("- [")
		b.WriteString(e.ID)
		b.WriteString("] ")
		b.WriteString(e.Content)
		b.WriteString("\n")
	}
	return os.WriteFile(p.path, []byte(b.String()), 0o644)
}

// emptyPrimaryFile returns the content written when MEMORY.md is
// auto-created: YAML frontmatter with the current timestamp + version,
// followed by an empty body.
func emptyPrimaryFile() string {
	fm := primaryFrontmatter{
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
		Version:     PrimaryVersion,
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	return b.String()
}

// parsePrimary splits the file into YAML frontmatter + body and parses
// the body into entries. Lines that do not match the "- [id] content"
// format are ignored (blank lines, prose written by a human). Legacy
// files with an HTML comment header (no frontmatter) are still parsed
// for backward compatibility.
func parsePrimary(raw string) ([]domain.PrimaryEntry, error) {
	raw = strings.TrimSpace(raw)
	body := raw
	// Strip YAML frontmatter if present.
	if strings.HasPrefix(raw, "---") {
		rest := strings.TrimPrefix(raw, "---\n")
		if strings.HasPrefix(rest, "---") {
			// empty frontmatter
			body = strings.TrimSpace(strings.TrimPrefix(rest, "---"))
		} else {
			end := strings.Index(rest, "\n---")
			if end >= 0 {
				body = strings.TrimSpace(rest[end+4:])
			}
		}
	}
	var out []domain.PrimaryEntry
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "<!--") || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		rest := strings.TrimPrefix(line, "- [")
		close := strings.Index(rest, "]")
		if close < 0 {
			continue
		}
		id := rest[:close]
		content := strings.TrimSpace(rest[close+1:])
		if id == "" || content == "" {
			continue
		}
		out = append(out, domain.PrimaryEntry{
			ID:        id,
			Content:   content,
			UpdatedAt: time.Now().UTC(), // file format does not persist timestamps per entry
		})
	}
	return out, nil
}
