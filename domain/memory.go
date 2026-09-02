package domain

import (
	"strings"
	"time"
	"unicode"
)

// ---- Memory tiers ----
//
// Memory has three tiers. user.md (legacy name primary.md) is a small,
// always-injected working set (~1k tokens) holding the user's rules and
// preferences. soul.md is an equally sized (~1k tokens) always-injected
// document holding agent working knowledge — conventions, gotchas,
// decisions, references — curated by background improvers. Fragments are
// an unlimited, on-demand archive stored as one markdown file per entry
// under memory/fragments/. The background agent promotes durable facts
// into the documents; foreground agents save new observations as
// fragments and search fragments by content + metadata.

// PrimaryTokenCap is the soft token budget for the user tier (user.md,
// legacy primary.md). The store enforces a character approximation
// (4 chars ≈ 1 token) to keep the always-injected prefix small.
const PrimaryTokenCap = 1000

// PrimaryCharCap is the character approximation of PrimaryTokenCap.
const PrimaryCharCap = PrimaryTokenCap * 4

// AgentTokenCap is the soft token budget for the agent tier (soul.md).
// Combined with the user tier the always-injected memory prefix totals
// ~2k tokens.
const AgentTokenCap = 1000

// AgentCharCap is the character approximation of AgentTokenCap.
const AgentCharCap = AgentTokenCap * 4

// Memory tier identifiers used in tool wire payloads and announcements.
const (
	MemoryTierPrimary  = "primary" // legacy alias for the user tier
	MemoryTierUser     = "user"
	MemoryTierAgent    = "agent"
	MemoryTierFragment = "fragment"
)

// PrimaryEntry is one line in the primary primary.md file. Primary
// entries are short, durable facts promoted from fragments by the
// background review agent. Foreground agents can read and update them
// but cannot create new ones directly.
type PrimaryEntry struct {
	ID        string    // stable id (fragment id the entry was promoted from, or generated)
	Content   string    // one line of text
	Source    string    // "agent" (promoted by review agent) | "user"
	UpdatedAt time.Time // last update time
}

// PrimaryMemory is the full primary memory document. It is loaded once
// at startup and re-read on each update so the hydration prefix stays
// in sync with the file on disk.
type PrimaryMemory struct {
	Entries   []PrimaryEntry
	UpdatedAt time.Time
}

// FragmentCategory constants define the high-level buckets a fragment
// can belong to. The category drives UI grouping and default metadata
// filters in search.
const (
	FragmentCategoryProject = "project" // project/task notes tied to a workspace
	FragmentCategoryUser    = "user"    // user-profile facts (preferences, habits)
	FragmentCategoryTask    = "task"    // task-specific observations
	FragmentCategoryGeneral = "general" // anything that does not fit the above
)

// NormalizeMemoryContent returns the canonical form used for exact memory
// duplicate checks. It normalizes line endings, removes trailing whitespace
// from each line, and trims the document boundary without changing internal
// indentation or case-sensitive content such as paths, symbols, and commands.
func NormalizeMemoryContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// MemoryFragment is one searchable memory archive entry, stored as a
// single markdown file under memory/fragments/<id>.md. The YAML
// frontmatter carries the metadata used for filtering; the body is the
// content the agent reads when the fragment is retrieved.
type MemoryFragment struct {
	ID        string   // filename stem (ulid)
	Category  string   // project | user | task | general
	Project   string   // optional workspace/project label
	Task      string   // optional task label
	Tags      []string // free-form tags
	Source    string   // "agent" | "user"
	Content   string   // markdown body
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FragmentSearchFilter narrows a fragment search by metadata. All
// non-zero fields are AND-combined with the content query.
type FragmentSearchFilter struct {
	Query    string   // BM25 content query (empty = list by metadata only)
	Category string   // exact match
	Project  string   // exact match
	Task     string   // exact match
	Tags     []string // entry must contain ALL tags
	Limit    int      // default 20, max 100
}

// FragmentSearchHit is one ranked result from a fragment search.
type FragmentSearchHit struct {
	Fragment *MemoryFragment
	Score    float64 // BM25 score (0 when query is empty)
}
