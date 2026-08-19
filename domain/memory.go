package domain

import "time"

// ---- Two-tier memory ----
//
// Primary memory is a small, always-injected working set (~1k tokens)
// stored in a single MEMORY.md file. Fragments are an unlimited,
// on-demand archive stored as one markdown file per entry under
// memories/fragments/. The background review agent promotes durable
// facts from fragments into primary; foreground agents save new
// observations as fragments and search fragments by content + metadata.

// PrimaryTokenCap is the soft token budget for primary memory. The
// primary store enforces a character approximation (4 chars ≈ 1 token)
// to keep the always-injected prefix small.
const PrimaryTokenCap = 1000

// PrimaryCharCap is the character approximation of PrimaryTokenCap.
const PrimaryCharCap = PrimaryTokenCap * 4

// PrimaryEntry is one line in the primary MEMORY.md file. Primary
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

// MemoryFragment is one searchable memory archive entry, stored as a
// single markdown file under memories/fragments/<id>.md. The YAML
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
