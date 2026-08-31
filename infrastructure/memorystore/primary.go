// Package memorystore implements the two-tier memory persistence
// adapters: a single primary.md file for the always-injected primary
// working set, and one markdown file per entry under memory/fragments/
// for the unlimited searchable archive. Both adapters auto-create their
// files and directories on first use.
package memorystore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// ---- Primary store (primary.md) ----

// PrimaryFile is the on-disk path of the primary memory document.
const PrimaryFile = "memory/primary.md"

// PrimaryVersion is the schema version of the primary.md frontmatter.
// Bump when the file format changes in a backward-incompatible way.
const PrimaryVersion = 2

// primaryFrontmatter is the YAML metadata block at the top of primary.md.
// It carries the last-updated timestamp and schema version so a human
// (or migration tool) can tell when the file was last touched and what
// format version it uses.
type primaryFrontmatter struct {
	LastUpdated string `yaml:"last_updated"`
	Version     int    `yaml:"version"`
}

// Primary is the PrimaryStore adapter backed by primary.md. The file is
// a single markdown document with YAML frontmatter (last_updated +
// version) followed by the body — a free-form prose document the agent
// edits in place via memory op=replace. Think of it as a README the agent
// maintains about the user and working context:
//
//	---
//	last_updated: "2026-08-20T00:00:00Z"
//	version: 2
//	---
//
//	You are a backend developer living in Jakarta...
//	You work on NusaShell and prefer pragmatic solutions...
//
// The entire body is treated as one entry; paragraphs are part of the
// same document, not separate entries. The ID is derived from a content
// hash so it survives reload with a stable ID without being stored.
type Primary struct {
	mu     sync.RWMutex
	path   string
	entry  domain.PrimaryEntry
	loaded bool
}

// NewPrimary opens (or auto-creates) the primary.md file at dataDir and
// loads its body into memory. The file is created empty with YAML
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
		p.entry = domain.PrimaryEntry{}
		p.loaded = true
		return nil
	}
	entry, err := parsePrimary(string(raw))
	if err != nil {
		return err
	}
	p.entry = entry
	p.loaded = true
	return nil
}

// Load returns the current primary memory document. It re-reads the
// file from disk so callers always see the latest state.
func (p *Primary) Load() *domain.PrimaryMemory {
	_ = p.load(false)
	p.mu.RLock()
	defer p.mu.RUnlock()
	return &domain.PrimaryMemory{
		Entries:   []domain.PrimaryEntry{p.entry},
		UpdatedAt: p.entry.UpdatedAt,
	}
}

// Path returns the absolute filesystem path of the primary.md file.
func (p *Primary) Path() string { return p.path }

// Update replaces the entire primary document body and rewrites the file.
// Used by memory op=replace target=primary when the agent rewrites the whole
// document. Returns an error if the new content would exceed PrimaryCharCap.
func (p *Primary) Update(entries []domain.PrimaryEntry) error {
	var content string
	for _, e := range entries {
		if content != "" {
			content += "\n\n"
		}
		content += e.Content
	}
	if len(content) > domain.PrimaryCharCap {
		return fmt.Errorf("primary memory at %d/%d chars; primary is capped at ~%d tokens", len(content), domain.PrimaryCharCap, domain.PrimaryTokenCap)
	}
	p.mu.Lock()
	p.entry = domain.PrimaryEntry{
		ID:        primaryID(content),
		Content:   content,
		UpdatedAt: clock.NewTime().Time(),
	}
	err := p.writeFile()
	p.mu.Unlock()
	return err
}

// Replace performs a substring-match update on the document body. The
// first occurrence of oldText is replaced with content. Used by
// foreground agents via memory op=replace target=primary.
func (p *Primary) Replace(oldText, content string) error {
	oldText = strings.TrimSpace(oldText)
	content = strings.TrimSpace(content)
	if oldText == "" || content == "" {
		return fmt.Errorf("oldText and content are required")
	}
	_ = p.load(false)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.Contains(p.entry.Content, oldText) {
		return fmt.Errorf("no primary text matching %q", oldText)
	}
	newBody := strings.Replace(p.entry.Content, oldText, content, 1)
	if len(newBody) > domain.PrimaryCharCap {
		return fmt.Errorf("primary memory at %d/%d chars; update would exceed the ~%d token cap", len(newBody), domain.PrimaryCharCap, domain.PrimaryTokenCap)
	}
	p.entry = domain.PrimaryEntry{
		ID:        primaryID(newBody),
		Content:   newBody,
		UpdatedAt: clock.NewTime().Time(),
	}
	return p.writeFile()
}

// writeFile serializes the current document to the markdown file with
// YAML frontmatter (last_updated + version). The body is written as-is
// so the file reads as clean writing when opened by hand. Caller must
// hold p.mu.
func (p *Primary) writeFile() error {
	fm := primaryFrontmatter{
		LastUpdated: clock.NewTime().RFC3339(),
		Version:     PrimaryVersion,
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	b.WriteString(p.entry.Content)
	if p.entry.Content != "" && !strings.HasSuffix(p.entry.Content, "\n") {
		b.WriteString("\n")
	}
	return os.WriteFile(p.path, []byte(b.String()), 0o644)
}

// emptyPrimaryFile returns the content written when primary.md is
// auto-created: YAML frontmatter with the current timestamp + version,
// followed by an empty body.
func emptyPrimaryFile() string {
	fm := primaryFrontmatter{
		LastUpdated: clock.NewTime().RFC3339(),
		Version:     PrimaryVersion,
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	return b.String()
}

// parsePrimary splits the file into YAML frontmatter + body and returns
// the entire body as a single entry. The ID is derived from a content
// hash so it survives reload with a stable ID without being stored.
func parsePrimary(raw string) (domain.PrimaryEntry, error) {
	raw = strings.TrimSpace(raw)
	body := raw
	// Strip YAML frontmatter if present.
	if strings.HasPrefix(raw, "---") {
		rest := strings.TrimPrefix(raw, "---\n")
		if strings.HasPrefix(rest, "---") {
			body = strings.TrimSpace(strings.TrimPrefix(rest, "---"))
		} else {
			end := strings.Index(rest, "\n---")
			if end >= 0 {
				body = strings.TrimSpace(rest[end+4:])
			}
		}
	}
	if body == "" {
		return domain.PrimaryEntry{}, nil
	}
	return domain.PrimaryEntry{
		ID:        primaryID(body),
		Content:   body,
		UpdatedAt: clock.NewTime().Time(),
	}, nil
}

// primaryID derives a deterministic ID from content so the entry survives
// reload with a stable ID even though the file does not store it.
func primaryID(content string) string {
	h := sha256.Sum256([]byte(content))
	return "prim_" + hex.EncodeToString(h[:8])
}
