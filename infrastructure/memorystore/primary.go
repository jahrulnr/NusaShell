// Package memorystore implements the three-tier memory persistence
// adapters: two single-file documents (memory/user.md for the user
// tier, legacy name primary.md, and memory/soul.md for the agent tier),
// plus one markdown file per entry under memory/fragments/ for the
// unlimited searchable archive. All adapters auto-create their files and
// directories on first use.
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

// ---- Document stores (user.md / soul.md) ----

// PrimaryFile is the on-disk path of the user-tier memory document. It was
// introduced as memory/primary.md; the file moved to user.md when the
// always-injected memory split into user + agent tiers. The move is done
// by hand (see data-locations.md) — no automatic migration runs.
const PrimaryFile = "memory/user.md"

// SoulFile is the on-disk path of the agent-tier memory document. The
// user-facing filename is soul.md so it cannot be confused with a repository
// AGENTS.md instruction file.
const SoulFile = "memory/soul.md"

// AgentFile is retained as a source-compatible alias for the agent-tier
// document. It resolves to soul.md; no memory/agent.md file is read or
// created.
const AgentFile = SoulFile

// DocVersion is the schema version of the document frontmatter. Bump when
// the file format changes in a backward-incompatible way.
const DocVersion = 2

// PrimaryVersion is kept as an alias for code and tests that reference the
// legacy constant name.
const PrimaryVersion = DocVersion

// docFrontmatter is the YAML metadata block at the top of a document file.
// It carries the last-updated timestamp and schema version so a human
// (or migration tool) can tell when the file was last touched and what
// format version it uses.
type docFrontmatter struct {
	LastUpdated string `yaml:"last_updated"`
	Version     int    `yaml:"version"`
}

// Document is a single markdown memory document with YAML frontmatter
// (last_updated + version) followed by the body — a free-form prose
// document the agent edits in place via memory op=replace. Think of it as
// a README the agent maintains: user.md about the user and working
// context, soul.md about agent working knowledge (conventions, gotchas,
// decisions, references).
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
type Document struct {
	mu     sync.RWMutex
	path   string // absolute file path
	cap    int    // character cap (token cap * 4)
	kind   string // "user" | "agent" (used in errors)
	entry  domain.PrimaryEntry
	loaded bool
}

// Primary is a compatibility alias for Document (the user tier store).
type Primary = Document

// newDocument opens (or auto-creates) a doc store at path with the given
// token cap and loads its body into memory. The file is created empty with
// YAML frontmatter if it does not exist.
func newDocument(path, kind string, tokenCap int) (*Document, error) {
	d := &Document{path: path, cap: tokenCap * 4, kind: kind}
	if err := d.load(true); err != nil {
		return nil, err
	}
	return d, nil
}

// NewPrimary opens (or auto-creates) the user-tier memory document
// (memory/user.md) at dataDir and loads its body into memory. A pre-split
// memory/primary.md is NOT migrated automatically; move it to
// memory/user.md by hand when upgrading (see data-locations.md).
func NewPrimary(dataDir string) (*Document, error) {
	return newDocument(filepath.Join(dataDir, PrimaryFile), domain.MemoryTierUser, domain.PrimaryTokenCap)
}

// NewAgent opens (or auto-creates) the agent-tier memory document
// (memory/soul.md) at dataDir and loads its body into memory.
func NewAgent(dataDir string) (*Document, error) {
	return newDocument(filepath.Join(dataDir, SoulFile), domain.MemoryTierAgent, domain.AgentTokenCap)
}

// load reads the file from disk, creating it empty if create is true
// and the file is missing. Safe to call repeatedly; subsequent calls
// re-read the file so updates from other processes are picked up.
func (d *Document) load(create bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, err := os.ReadFile(d.path)
	if err != nil {
		if !create || !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(d.path, []byte(emptyDocFile()), 0o644); err != nil {
			return err
		}
		d.entry = domain.PrimaryEntry{}
		d.loaded = true
		return nil
	}
	entry, err := parseDoc(string(raw))
	if err != nil {
		return err
	}
	d.entry = entry
	d.loaded = true
	return nil
}

// Load returns the current memory document. It re-reads the file from
// disk so callers always see the latest state.
func (d *Document) Load() *domain.PrimaryMemory {
	_ = d.load(false)
	d.mu.RLock()
	defer d.mu.RUnlock()
	return &domain.PrimaryMemory{
		Entries:   []domain.PrimaryEntry{d.entry},
		UpdatedAt: d.entry.UpdatedAt,
	}
}

// Path returns the absolute filesystem path of the document file.
func (d *Document) Path() string { return d.path }

// Update replaces the entire document body and rewrites the file.
// Used by memory op=replace target=user|agent when the agent rewrites the
// whole document. Returns an error if the new content would exceed the
// tier's token cap.
func (d *Document) Update(entries []domain.PrimaryEntry) error {
	var content string
	for _, e := range entries {
		if content != "" {
			content += "\n\n"
		}
		content += e.Content
	}
	if len(content) > d.cap {
		return fmt.Errorf("%s memory at %d/%d chars; %s tier is capped at ~%d tokens", d.kind, len(content), d.cap, d.kind, d.cap/4)
	}
	d.mu.Lock()
	d.entry = domain.PrimaryEntry{
		ID:        docID(content),
		Content:   content,
		UpdatedAt: clock.NewTime().Time(),
	}
	err := d.writeFile()
	d.mu.Unlock()
	return err
}

// Replace performs a substring-match update on the document body. The
// first occurrence of oldText is replaced with content. Used by
// foreground agents via memory op=replace target=user|agent.
func (d *Document) Replace(oldText, content string) error {
	oldText = strings.TrimSpace(oldText)
	content = strings.TrimSpace(content)
	if oldText == "" || content == "" {
		return fmt.Errorf("oldText and content are required")
	}
	_ = d.load(false)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !strings.Contains(d.entry.Content, oldText) {
		return fmt.Errorf("no %s memory text matching %q", d.kind, oldText)
	}
	newBody := strings.Replace(d.entry.Content, oldText, content, 1)
	if len(newBody) > d.cap {
		return fmt.Errorf("%s memory at %d/%d chars; update would exceed the ~%d token cap", d.kind, len(newBody), d.cap, d.cap/4)
	}
	d.entry = domain.PrimaryEntry{
		ID:        docID(newBody),
		Content:   newBody,
		UpdatedAt: clock.NewTime().Time(),
	}
	return d.writeFile()
}

// writeFile serializes the current document to the markdown file with
// YAML frontmatter (last_updated + version). The body is written as-is
// so the file reads as clean writing when opened by hand. Caller must
// hold d.mu.
func (d *Document) writeFile() error {
	fm := docFrontmatter{
		LastUpdated: clock.NewTime().RFC3339(),
		Version:     DocVersion,
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	b.WriteString(d.entry.Content)
	if d.entry.Content != "" && !strings.HasSuffix(d.entry.Content, "\n") {
		b.WriteString("\n")
	}
	return os.WriteFile(d.path, []byte(b.String()), 0o644)
}

// emptyDocFile returns the content written when a document file is
// auto-created: YAML frontmatter with the current timestamp + version,
// followed by an empty body.
func emptyDocFile() string {
	fm := docFrontmatter{
		LastUpdated: clock.NewTime().RFC3339(),
		Version:     DocVersion,
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	return b.String()
}

// parseDoc splits the file into YAML frontmatter + body and returns the
// entire body as a single entry. The ID is derived from a content hash so
// it survives reload with a stable ID without being stored.
func parseDoc(raw string) (domain.PrimaryEntry, error) {
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
		ID:        docID(body),
		Content:   body,
		UpdatedAt: clock.NewTime().Time(),
	}, nil
}

// docID derives a deterministic ID from content so the entry survives
// reload with a stable ID even though the file does not store it.
func docID(content string) string {
	h := sha256.Sum256([]byte(content))
	return "prim_" + hex.EncodeToString(h[:8])
}
