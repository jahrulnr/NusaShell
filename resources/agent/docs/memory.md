# Memory

NusaShell has a two-tier memory system: a small, always-injected
**primary** document and an unlimited, searchable **fragments**
archive.

## Two tiers

| Tier | Storage | Cap | Injected | Tools |
|---|---|---|---|---|
| **Primary** | `memory/primary.md` (single markdown document) | ~1k tokens | Every turn (via hydration) | `file_read(path="memory/primary.md")`, `memory(op="replace",target="primary")` |
| **Fragments** | `memory/fragments/*.md` (one file per entry) | Unlimited | On-demand (search) | `memory(op="save")`, `memory(op="search")`, `memory(op="list",target="fragments")`, `memory(op="replace",target="fragment")`, `memory(op="delete")` |

Primary memory is a single markdown document — like a README the agent
maintains about the user and working context. It is injected into every
turn. Fragments are the cold archive — all new facts enter here first,
and the background review agent edits the primary document to reflect
the most durable facts.

Fragments are **user-only**. Durable facts about the active workspace
belong in `memory_project` (see `memory-project.md`), not in fragments
with `category=project`.

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
`memory(op="replace", target="primary")` (substring match) or rewrites
the whole body by omitting `old_text`.

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
project, task, tags) — the same ranking approach the docs corpus search
uses.

## Tools

All memory operations go through the `memory` dispatcher tool; `op` selects:

- `save` — save a new fact as a **fragment** (`content`, `category`,
  optional `project`, `task`, `tags`). All new facts enter fragments first.
  Exact normalized duplicates are idempotent.
- `search` — search fragments by content (BM25) with optional
  metadata filters (`query`, `category`, `project`, `task`, `tags`,
  `limit`). Returns ranked results with scores.
- `list` — list the fragment archive (`target="fragments"`, the default)
  with optional metadata filters. Primary is a single document, not a
  list — read it with `file_read(path="memory/primary.md")` instead.
- `replace` — update memory. For primary: `target="primary"` +
  `old_text` (substring match) + `content` to edit part of the document,
  or omit `old_text` to rewrite the entire body. For fragments:
  `target="fragment"` + `id` + `content`.
- `delete` — delete a fragment by `id`.

## When to save

Treat `memory(op="save")` as a deliberate commit, not a default. Before
saving, apply two tests:

1. **Lookup test:** would a future conversation plausibly need to look this
   up rather than re-derive it in seconds?
2. **Source test:** would that conversation find the fact faster in docs,
   skills, code, or the current conversation than by searching memory?

Save only when both answers favor memory — the fact is durable, not already
captured elsewhere, and likely to be searched for. Run `memory(op="search")`
first; if a matching fragment exists, replace it instead of adding
a duplicate. A redundant fragment is noise the background review agent must
triage, and it pushes the real fact down in search results.

Good examples:

    memory(op="save", content="User prefers Indonesian for code comments", category="user", tags=["preference"])
    memory(op="search", query="comment language")   # before saving, check for duplicates
    memory_project(op="query", topic="deploy")      # project facts live in memory_project

## How the review agent edits primary

The background review agent edits the primary document via
`memory(op="replace", target="primary")` when it finds durable facts in fragments
that belong in the always-injected working set. Primary is capped at ~1k
tokens, so the agent rewrites or trims stale text before adding new
content.

The review agent sees the current primary document as a pre-injected
`file_read` tool result (reading `memory/primary.md` directly, frontmatter
included) at the start of each review run, so it can avoid duplicates and
spot stale text without needing to read the file itself first. Reviews are bounded to a small number of
tool rounds and coalesce concurrent threshold/skill/compaction triggers, so a burst cannot launch duplicate reviews or replay the same transcript repeatedly. Activity that arrives while a review is running is retained for one follow-up review. Both successful and failed reviews enter the cooldown period to prevent redundant re-review of the same window. Exact duplicate fragment writes are idempotent.

## How the review agent gets the transcript

The review agent receives the conversation transcript as a pre-injected
`review_transcript` synthetic tool result. The JSON contains proper role
alternation (user/assistant), nested tool calls with their arguments and
outputs, conversation metadata, and the absolute path to the full
conversation JSON file (use `file_read` on that path if the bounded
segment lacks context). This is NOT a flat text dump — the LLM sees the
conversation semantically, the same way it would see a tool result from
any other tool.

The transcript is **incrementally bounded**: each review only processes
messages since the last review (tracked via `last_reviewed_msg_count` on
the conversation). This prevents re-reading and re-reasoning over
already reviewed content. The `review_transcript` tool and the primary
`file_read` result are pre-injected before the first LLM call — the agent
does not need to call them to get the initial data.

`review_transcript` is review-only: it is not registered in the global
Toolbox and is executed locally by the review loop. The primary memory
injection uses the real `file_read` tool (which IS in the global Toolbox
and whitelisted for the review agent), so the agent can re-read the file
itself if needed.

`model_override` is also review-only and executed locally. It lets the
review agent correct a model's catalog metadata (vision, context window,
max output, etc.) for a specific provider+model pair when the transcript
shows the catalog is wrong. Corrections are stored in
`learning/model_overrides.json`, survive catalog re-imports, and win over
both catalog and auto-learned values at model resolution time. See
`providers.md` for the precedence rules.

## Review triggers

Two independent triggers fire the background review:

1. **Turn threshold** (`learning_review_threshold`, default 10 user turns) —
   fires after N user turns. Set 0 to disable turn-based review.
2. **Skill nudge** (`skill_nudge_interval`, default 15 tool calls) — fires
   after N tool calls across all turns. Catches tool-heavy coding sessions
   that don't reach the turn threshold. Set 0 to disable.

Both triggers share the same reservation gate, so a threshold and a skill nudge
firing at the same time produce only one review. A trigger that is deferred
(cooldown active or a review already running) still resets its counter, so it
does not re-fire on every subsequent turn/tool call during the cooldown
window. When new activity arrives while a review is running, it is coalesced
and a single follow-up review runs immediately after the in-flight review
finishes (the cooldown is skipped for that one follow-up). Compaction-triggered
review uses the same gate. Retry cooldown applies after a failed review, and
after a successful review that had no coalesced activity.

Good examples:

    memory(op="replace", target="primary", old_text="old CI config notes", content="CI uses GitHub Actions + GitLab Runner")
    memory(op="replace", target="primary", content="Full rewrite of the primary document body…")

## What not to save

- Temporary debugging steps or error workarounds
- One-time task instructions
- Information already captured in skills, memory, or documentation
- Sensitive credentials or API keys
- Transient state (e.g. "pipeline failed: missing env var")

Temporary, task-scoped notes have a dedicated home: the `todo` tool's
`brief` argument. It is the per-task living planning document —
## Objective / ## Findings / ## Approach / ## Done when — and it
survives compaction for the current conversation. Update it as findings
emerge. Keep scratch observations there instead of saving them to memory.

Bad examples:

    memory(op="save", content="User asked me to fix the bug at 14:32")            # transient
    memory(op="save", content="The API key format for provider X is sk-...")       # secrets
    memory(op="save", content="pipeline failed: missing env var")                 # one-off state
    memory(op="save", content="NusaShell is a Go binary with Clean Architecture") # already in docs
