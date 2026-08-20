# Memory

NusaShell has a two-tier memory system: a small, always-injected
**primary** document and an unlimited, searchable **fragments**
archive.

## Two tiers

| Tier | Storage | Cap | Injected | Tools |
|---|---|---|---|---|
| **Primary** | `memory/primary.md` (single markdown document) | ~1k tokens | Every turn (via hydration) | `memory_list target=primary`, `memory_replace target=primary` |
| **Fragments** | `memory/fragments/*.md` (one file per entry) | Unlimited | On-demand (search) | `memory_save`, `memory_search`, `memory_list target=fragments`, `memory_replace target=fragment`, `memory_delete` |

Primary memory is a single markdown document — like a README the agent
maintains about the user and working context. It is injected into every
turn. Fragments are the cold archive — all new facts enter here first,
and the background review agent edits the primary document to reflect
the most durable facts.

## Primary document format

`memory/primary.md` is a markdown file with YAML frontmatter followed by
a free-form prose body:

```markdown
---
last_updated: "2026-08-20T00:00:00Z"
version: 2
---

You are a backend developer living in Jakarta. You prefer pragmatic
solutions over over-engineered architectures.

You work on NusaShell and value clean architecture, Go, and tooling
that works for both humans and AI agents.
```

The entire body is one document — paragraphs are part of the same
entry, not separate entries. The agent edits the body in place via
`memory_replace target=primary` (substring match) or rewrites the whole
body by omitting `old_text`.

## Fragment metadata

Each fragment is a markdown file with YAML frontmatter:

```yaml
---
id: frag_01J…
category: project          # project | user | task | general
project: nusashell         # optional workspace/project label
task: memory-tier          # optional task label
tags: [go, arch]           # optional free-form tags
source: agent              # agent | user
created_at: 2026-08-19T12:00:00Z
updated_at: 2026-08-19T12:30:00Z
---
The repo uses Go with Clean Architecture and strict layer dependencies.
```

Search combines BM25 content ranking with metadata filters (category,
project, task, tags) — like `docs_search` but for memory.

## Tools

- `memory_save` — save a new fact as a **fragment** (`content`, `category`,
  optional `project`, `task`, `tags`). All new facts enter fragments first.
- `memory_search` — search fragments by content (BM25) with optional
  metadata filters (`query`, `category`, `project`, `task`, `tags`,
  `limit`). Returns ranked results with scores.
- `memory_list` — list entries. `target="primary"` returns the primary
  document; `target="fragments"` (default) lists the archive with
  optional metadata filters.
- `memory_replace` — update memory. For primary: `target="primary"` +
  `old_text` (substring match) + `content` to edit part of the document,
  or omit `old_text` to rewrite the entire body. For fragments:
  `target="fragment"` + `id` + `content`.
- `memory_delete` — delete a fragment by `id`.

## When to save

Treat `memory_save` as a deliberate commit, not a default. Before saving,
apply two tests:

1. **Lookup test:** would a future conversation plausibly need to look this
   up rather than re-derive it in seconds?
2. **Source test:** would that conversation find the fact faster in docs,
   skills, code, or the current conversation than by searching memory?

Save only when both answers favor memory — the fact is durable, not already
captured elsewhere, and likely to be searched for. Run `memory_search`
first; if a matching fragment exists, `memory_replace` it instead of adding
a duplicate. A redundant fragment is noise the background review agent must
triage, and it pushes the real fact down in search results.

Good examples:

    memory_save(content="User prefers Indonesian for code comments", category="user", tags=["preference"])
    memory_save(content="Repo policy: ignore untracked folders in root — they are research scratch", category="project", tags=["repo-policy"])
    memory_search(query="comment language")   # before saving, check for duplicates

## How the review agent edits primary

The background review agent edits the primary document via
`memory_replace target=primary` when it finds durable facts in fragments
that belong in the always-injected working set. Primary is capped at ~1k
tokens, so the agent rewrites or trims stale text before adding new
content.

The review agent sees the current primary document injected into its
system prompt at the start of each review run, so it can avoid
duplicates and spot stale text without needing to call
`memory_list target=primary` first.

Good examples:

    memory_replace(target="primary", old_text="old CI config notes", content="CI uses GitHub Actions + GitLab Runner")
    memory_replace(target="primary", content="Full rewrite of the primary document body…")

## What not to save

- Temporary debugging steps or error workarounds
- One-time task instructions
- Information already captured in skills, memory, or documentation
- Sensitive credentials or API keys
- Transient state (e.g. "pipeline failed: missing env var")

Bad examples:

    memory_save(content="User asked me to fix the bug at 14:32")            # transient
    memory_save(content="The API key format for provider X is sk-...")       # secrets
    memory_save(content="pipeline failed: missing env var")                 # one-off state
    memory_save(content="NusaShell is a Go binary with Clean Architecture") # already in docs
