# Memory

NusaShell has a two-tier memory system: a small, always-injected
**primary** working set and an unlimited, searchable **fragments**
archive.

## Two tiers

| Tier | Storage | Cap | Injected | Tools |
|---|---|---|---|---|
| **Primary** | `memory/primary.md` (single markdown file) | ~1k tokens | Every turn (via hydration) | `memory_list target=primary`, `memory_replace target=primary`, `memory_demote` |
| **Fragments** | `memory/fragments/*.md` (one file per entry) | Unlimited | On-demand (search) | `memory_save`, `memory_search`, `memory_list target=fragments`, `memory_replace target=fragment`, `memory_delete`, `memory_promote` |

Primary memory is the hot set — the durable, frequently-needed facts the
agent sees in every turn. Fragments are the cold archive — all new facts
enter here first, and the background review agent promotes the most
durable ones into primary.

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
- `memory_list` — list entries. `target="primary"` lists the always-injected
  working set; `target="fragments"` (default) lists the archive with
  optional metadata filters.
- `memory_replace` — update an existing entry. For primary: `target="primary"`
  + `old_text` (substring match) + `content`. For fragments: `target="fragment"`
  + `id` + `content`.
- `memory_delete` — delete a fragment by `id`. Primary entries cannot be
  deleted (use `memory_demote` to move them back to fragments).
- `memory_promote` — move a fragment into **primary memory**. Use when a
  fragment contains a durable, frequently-needed fact. Background review
  agent only.
- `memory_demote` — move a primary entry back to fragments. Use when a
  primary entry is stale or no longer frequently needed. Background review
  agent only.

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

## When to promote

The background review agent promotes fragments into primary memory when
they are durable and frequently needed. Primary is capped at ~1k tokens,
so be selective. When primary is near its cap, demote stale entries
before promoting new ones.

The review agent sees the current primary memory content injected into
its system prompt at the start of each review run, so it can avoid
promoting duplicates and spot stale entries to demote without needing to
call `memory_list target=primary` first.

Good examples:

    memory_promote(id="frag_01J…")   # "User prefers Indonesian" — durable, every-turn fact
    memory_demote(old_text="old CI config")  # stale, no longer needed in primary

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
