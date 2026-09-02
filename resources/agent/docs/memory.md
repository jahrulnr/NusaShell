# Memory

NusaShell has a three-tier memory system: two small, always-injected
documents — **user.md** (user rules and preferences; legacy name
`primary.md`) and **soul.md** (agent working knowledge) — plus an
unlimited, searchable **fragments** archive.

## Tiers

| Tier | Storage | Cap | Injected | Tools |
|---|---|---|---|---|
| **User** | `memory/user.md` (single markdown document; legacy path `memory/primary.md` — move it here by hand when upgrading) | ~1k tokens | Every turn (via hydration) | `file_read(path="memory/user.md")`, `memory(op="replace",target="user")` (legacy `target="primary"` aliases user) |
| **Agent / Soul** | `memory/soul.md` (single markdown document; legacy path `memory/agent.md` — move it here by hand when upgrading) | ~1k tokens | Every turn (via hydration) | `file_read(path="memory/soul.md")`, `memory(op="replace",target="agent")` |
| **Fragments** | `memory/fragments/*.md` (one file per entry) | Unlimited | On-demand (search + `task_memory` announcements) | `memory(op="save")`, `memory(op="search")`, `memory(op="list",target="fragments")`, `memory(op="replace",target="fragment")`, `memory(op="delete")` |

The Agent / Soul tier keeps the wire identifier `agent` for memory operations
and RPCs; only its on-disk filename and Learning label are `soul.md` / Soul.

Both documents are single markdown files — like READMEs: user.md about
the user and working context, soul.md about agent working knowledge
(conventions, gotchas, decisions, references) curated by the background
improver. Hydration reads each non-empty document with its own real
`file_read` call/result pair, so the persisted transcript records
`memory/user.md` and `memory/soul.md` separately. A workspace `AGENTS.md`
is repository guidance and is injected as a separate `file_read` call; it is
not NusaShell memory. Together the memory documents inject ~2k tokens every
turn. Fragments are the cold archive — all new facts enter here first.

Generated timestamps use the host machine's local timezone and RFC3339 offset;
the examples below use `Asia/Jakarta` (`+07:00`).

Fragments are **user-only**. Durable facts about the active workspace
belong in `memory_project` (see `memory-project.md`), not in fragments
with `category=project`.

The Learning knowledge graph rebuilds `related` links from content similarity
and specific fragment metadata (project, task, and non-ubiquitous tags). It
also records `used_with` links when successful agent or background-review
tools observe multiple memory/skill nodes in the same turn. Edges whose
endpoints were deleted are removed on the next graph rebuild. Node size is
scaled from the number of unique neighbouring nodes, so the most-connected
memory or skill nodes appear as the largest hubs; hover text includes the
relation count. When the graph is zoomed out, the renderer preserves most of
the relation-driven size contrast so nearby degree values do not collapse to
the same rasterized radius. Dense edge lines stay thin, and a completed full
layout places highly connected hubs toward the center while low-degree and
isolated nodes move toward a compact perimeter ring without detaching them
from the main cluster. Its restrained archipelago palette maps skills to
ocean blue, fragments to earth brown, primary memory to leaf green, and graph
edges to deeper ocean, mangrove, or sand tones. Position-preserving background
refreshes do not re-run this radial pass.

## Document format (user.md / soul.md)

Both tier documents share one format: a markdown file with YAML frontmatter
followed by a free-form prose body. Example (user.md):

```markdown
---
last_updated: "2026-08-20T00:00:00+07:00"
version: 2
---

You are a backend developer living in Jakarta. You prefer pragmatic
solutions over over-engineered architectures.

You work on NusaShell and value clean architecture, Go, and tooling
that works for both humans and AI agents.
```

The entire body is one document — paragraphs are part of the same
entry, not separate entries. The agent edits the body in place via
`memory(op="replace", target="user"|"agent")` (substring match) or
rewrites the whole body by omitting `old_text`. Legacy
`target="primary"` maps to the user tier.

Users can edit the two always-injected documents from **Learning**: the user
document is under **About You**, and `soul.md` is under **About Agent**. These
editors use the explicit `memory.primary.update` and `memory.agent.update`
RPCs, preserve tier-only semantics, and allow an intentional empty document.
Each editor shows the 4000-character cap; fragment memory is not changed by
either action.

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
created_at: 2026-08-19T12:00:00+07:00
updated_at: 2026-08-19T12:30:00+07:00
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
  with optional metadata filters. The tier documents are single files,
  not lists — read them with `file_read(path="memory/user.md")` or
  `file_read(path="memory/soul.md")` instead.
- `replace` — update memory. For tier documents: `target="user"` or
  `target="agent"` (legacy `"primary"` aliases user) + `old_text`
  (substring match) + `content` to edit part of the document, or omit
  `old_text` to rewrite the entire body. For fragments:
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

## How the background improver manages memory

The background improver (`AgentImprover`) is a hidden agent with the full
local toolbox plus `review_transcript`: it reads the conversation transcript
JSON and the files the room touched directly, researches the web when
needed, and writes durable knowledge through the normal `memory` tool —
**soul.md** for agent working knowledge and **fragments** for task facts.
It never writes `user.md`, never deletes memory, and honors a mutation cap
per run. Successful memory writes fan out to every conversation via the
announcement channel and to this room via `task_memory` announcements.
that belong in the always-injected working set. Primary is capped at ~1k
tokens, so the agent rewrites or trims stale text before adding new
content.

The review agent sees the current user document as a pre-injected
`file_read` tool result (reading `memory/user.md` directly, frontmatter
included) at the start of each review run, so it can avoid duplicates and
spot stale text without needing to read the file itself first. Reviews use the same provider retry policy as conversation turns, then hard-fail after the internal retry budget is exhausted or when the error is non-retryable. Their tool loop has no artificial round cap: it continues while the model returns tool calls and ends on a terminal response or error. Concurrent threshold/skill/compaction triggers are still coalesced, so a burst cannot launch duplicate reviews or replay the same transcript repeatedly. Activity that arrives while a review is running is retained for one follow-up review. Both successful and failed reviews enter the cooldown period to prevent redundant re-review of the same window. Exact duplicate fragment writes are idempotent.

Tool failures are fed back into the review conversation so the agent can
correct the call and continue. Provider transport failures are retried
internally; a non-retryable error or exhausted retry budget hard-fails the
background review. The Learning log shows only a concise failure status;
verbose diagnostics remain in the backend log and trajectory.

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

    memory(op="replace", target="primary", old_text="old automation notes", content="Automation uses a local runner")
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
