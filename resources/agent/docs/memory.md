# Memory

NusaShell has a three-tier memory system: two small, always-injected
documents — **user.md** (user rules and preferences) and **soul.md**
(agent working knowledge) — plus an
unlimited, searchable **fragments** archive.

## Tiers

| Tier | Storage | Cap | Injected | Tools |
|---|---|---|---|---|
| **User** | `memory/user.md` (single markdown document) | ~1k tokens | Every turn (via hydration) | `file_read(path="memory/user.md")`, `memory(op="replace",target="user")` |
| **Agent / Soul** | `memory/soul.md` (single markdown document) | ~1k tokens | Every turn (via hydration) | `file_read(path="memory/soul.md")`, `memory(op="replace",target="agent")` |
| **Fragments** | `memory/fragments/*.md` (one file per entry) | Unlimited | On-demand (search + `task_memory` announcements) | `memory(op="save")`, `memory(op="search")`, `memory(op="list",target="fragments")`, `memory(op="replace",target="fragment")`, `memory(op="delete")` |

The Agent / Soul tier keeps the wire identifier `agent` for memory operations
and RPCs; only its on-disk filename and Learning label are `soul.md` / Soul.

Both documents are single markdown files — like READMEs: user.md about
the user and working context, soul.md about agent working knowledge
(conventions, gotchas, decisions, references) curated by the background
learning agent. Hydration reads each non-empty document with its own real
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
ocean blue, fragments to earth brown, user memory to leaf green, and graph
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
rewrites the whole body by omitting `old_text`.

Users can edit the two always-injected documents from **Learning**: the user
document is under **About You**, and `soul.md` is under **About Agent**. These
editors use the explicit `memory.user.update` and `memory.agent.update`
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
  `target="agent"` + `old_text` (substring match) + `content` to edit
  part of the document, or omit `old_text` to rewrite the entire body.
  For fragments:
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
a duplicate. A redundant fragment is noise the background learning agent must
triage, and it pushes the real fact down in search results.

Good examples:

    memory(op="save", content="User prefers Indonesian for code comments", category="user", tags=["preference"])
    memory(op="search", query="comment language")   # before saving, check for duplicates
    memory_project(op="query", topic="deploy")      # project facts live in memory_project

## How the unified background learning agent manages memory

One background agent (`AgentReview`) runs after each completed conversation
segment. It is an agentic pass, not a one-shot extractor: it starts with the
conversation transcript plus the current `memory/user.md` and
`memory/soul.md` documents (both pre-injected as real `file_read` tool
results, frontmatter included), inspects the workspace with read-only file
tools, researches the web when needed, and then curates memory through the
normal `memory` tool.

What it manages:

- **soul.md** (`target="agent"`) — agent working knowledge: conventions,
  gotchas, decisions, references, recurring fixes. The agent adds, trims,
  and consolidates entries, always staying under the ~1k token cap.
- **user.md** (`target="user"`) — user rules, preferences, and stable
  context. The agent updates it only on clear, durable changes or explicit
  corrections from the conversation, and trims stale text.
- **fragments** — new durable facts enter here with category/tags;
  redundant entries are replaced, and fragments that are contradicted,
  stale, or obsolete are deleted when there is clear evidence.

The agent honors a mutation budget (at most 10 mutating memory/skill calls
per run), never saves transient task state, work logs, or secrets, and
treats interrupted tool runs as partial evidence. Memory writes fan out to
every conversation via the announcement channel and to the current room via
`task_memory` announcements.

The tool loop has no artificial round cap: it continues while the model
returns tool calls and ends on a terminal response or error. Concurrent
threshold/skill/compaction triggers are coalesced, so a burst cannot launch
duplicate runs or replay the same transcript repeatedly. Activity that
arrives while a run is in progress is retained for one follow-up run. Both
successful and failed runs enter the cooldown period.

Tool failures are fed back into the run so the agent can correct the call
and continue. Provider transport failures are retried internally; a
non-retryable error or exhausted retry budget hard-fails the run. The
Learning log shows only a concise failure status; verbose diagnostics
remain in the backend log and trajectory.

## How the agent gets the transcript

The agent receives the conversation transcript as a pre-injected
`review_transcript` synthetic tool result. The JSON contains proper role
alternation (user/assistant), nested tool calls with their arguments and
outputs, conversation metadata, and the absolute path to the full
conversation JSON file (use `file_read` on that path if the bounded
segment lacks context). This is NOT a flat text dump — the model sees the
conversation semantically, the same way it would see a tool result from
any other tool.

The transcript is **incrementally bounded**: each pass only processes
messages since the last pass (tracked via `last_reviewed_msg_count` on
the conversation). This prevents re-reading and re-reasoning over
already reviewed content. `review_transcript` and both memory document
`file_read` results are pre-injected before the first LLM call — the
agent does not need to call them to get the initial data.

`review_transcript` is background-only: it is not registered in the global
Toolbox and is executed locally by the learning loop. The memory document
injection uses the real `file_read` tool (which IS in the global Toolbox
and allowed for the background agent), so the agent can re-read the files
itself if needed.

`model_override` is also background-only and executed locally. It lets the
agent correct a model's catalog metadata (vision, context window, max
output, etc.) for a specific provider+model pair when the transcript shows
the catalog is wrong. Corrections are stored in
`learning/model_overrides.json`, survive catalog re-imports, and win over
both catalog and auto-learned values at model resolution time. See
`providers.md` for the precedence rules.

## Background agent tools and boundaries

The unified background agent advertises: the local `review_transcript` and
`model_override` tools; the `memory` and `skill` dispatchers (create,
update, delete included); read-only file inspection (`file_read`,
`file_list`, `file_info`, `find_file`, `grep`); web research
(`web_search`, `web_fetch`, `web_answer`); product docs; and read-only
`memory_project` (`query`/`list`/`read`) when the conversation has a
workspace.

It deliberately has **no** `exec`, no arbitrary file writes/deletes, no
automation or scheduling tools, and no subagent/delegate tools. Skill
deletion is restricted to agent-owned skills; user-owned and builtin skills
are protected by the store. Project memory is read-only for it.

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

    memory(op="replace", target="user", old_text="old automation notes", content="Automation uses a local runner")
    memory(op="replace", target="user", content="Full rewrite of the user document body…")

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
