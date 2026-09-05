# Memory

NusaShell memory is an experience-learning catalog plus two human-only
always-injected documents.

| Surface | Storage | Who writes | Injected |
|---|---|---|---|
| **About You** | `memory/user.md` | Human (Learning UI) | Every turn via `file_read` |
| **About Agent** | `memory/soul.md` | Human (Learning UI) | Every turn via `file_read` |
| **Records** | `growth/memories.jsonl` | Memory consolidator | Compact APPLY block (top-K, scoped) |
| **Experiences** | `growth/experiences.jsonl` | Runtime at `finishTurn` | Not injected; Learning UI list |

`user.md` and `soul.md` are human-only. Agents, the consolidator, and skill
jobs never `replace` or `update` them. Hydration still `file_read`s both
when they are non-empty. A workspace `AGENTS.md` is repository guidance and
is a separate `file_read`.

Durable facts, preferences, and constraints live as structured
**MemoryRecords** (`episode`, `fact`, `preference`, `constraint`,
`project_convention`, `environment_fact`, `belief`). Retired and superseded
records stay on disk for audit and are excluded from search and APPLY.

Workspace knowledge stays in `memory_project` (see `memory-project.md`).

## Consolidator

The memory consolidator runs as a background job when a high-signal
experience is recorded (explicit teaching, user correction, verified
recovery, repeated failure, or repeated procedure). It converts the
experience into typed operations (`memory.upsert`, `memory.strengthen`,
`memory.merge`, `memory.contradict`, `memory.retire`).

When a learning model is available (configured via `review_model` in
Settings, or the first enabled provider), the consolidator calls the LLM
with the RFC system prompt and a compact packet (experience JSON + related
memories + scope). The LLM returns typed JSON operations that are
validated and applied through `MemoryService.Apply`.

When no provider is available, the consolidator falls back to a
deterministic rule-based extraction (`teachingOps`) so the job still
produces output in offline/no-provider setups. The LLM path and the
deterministic path share the same deduplication and apply logic.

## The job transcript

The LLM call runs as a headless agent turn rather than a bare completion, so
the whole run is persisted as a `type=background` conversation: the packet
that was sent, every tool round, and the final answer. The job's Learning log
entry carries that conversation's id (`llm_conversation_id`) and its **View
LLM log** button opens it.

That transcript is the only record of *why* a job saved what it saved, so it
is kept even when the call failed or decided nothing was durable. Do not
confuse it with the entry's `conversation_id`, which is the user conversation
the job learned from.

## Agent tools

The `memory` dispatcher is read-only. `op` selects:

- `search` — substring/BM25 over retrievable records (`query`, optional
  `type`, `status`, `scope`, `project`, `limit`)
- `get` — one record by `id`
- `list` — retrievable records with the same filters

There is no `save`, `replace`, or `delete`. Explicit teaching
("remember…", "don't forget…") is recorded as an experience; the
consolidator commits typed records.

Good examples:

    memory(op="search", query="Go backend")
    memory(op="get", id="mem_01J…")
    memory(op="list", type="preference", limit=10)

Bad examples:

    memory(op="save", content="user prefers Go")
    memory(op="replace", target="user", content="…")
    memory(op="delete", id="mem_01J…")

When the user states a standing preference or correction, continue the
task. Do not try to persist it yourself.

## Hydration APPLY

After `memory_project` IDX, hydration may include a compact APPLY block of
top-K retrievable records with scope. Use it. Project-scoped lines override
broader user-level lines. Do not dump the catalog.

## Human UI

Learning keeps **About You** / **About Agent** editors
(`memory.user.update` / `memory.agent.update`). Structured records render
below those editors. Humans may **retire** a record; they do not edit or
promote records.

## Graph and search

The Learning graph links records and skills (`related` from overlap,
`used_with` from successful tools in one turn). Search fuses BM25,
optional embeddings, and graph expansion. Node colors: skills, records,
user document entries.
