# Memory

Memory is what a thread's model knows about the owner and their work that
did not come from this thread: who the owner is, how they want work done,
decisions and constraints of a project, and where things live. It is
written by the model (and the owner), survives compaction and new threads,
and is shown to every model and agent the owner runs.

This document records the design and why. It starts from what the systems
that already do this learned.

## What others do

| System | Shape | Lessons kept |
|---|---|---|
| Claude Code auto memory | One fact per file, typed (`user`, `feedback`, `project`, `reference`), a one-line-per-memory index (`MEMORY.md`) loaded every session up to 200 lines / 25 KB, bodies read on demand | Load a small index up front and the details just in time. Type memories by what they are for. Skip what the codebase, git, or instructions already say. Update an existing memory instead of adding a duplicate; delete one that turned out wrong. A memory is a claim from the past — verify a named file, flag, or function before recommending it. Convert relative dates to absolute ones. |
| Claude API memory tool | A `/memories` directory the model views first and edits with file commands | "Assume interruption": record progress outside the context window. The host owns validation, size caps, and expiry — "periodically delete memory files that haven't been accessed in a long time". |
| Claude app / ChatGPT | Saved memories plus chat history; separate memory per project; incognito / temporary chats write nothing | The owner must be able to see, edit, and delete every memory. Project-scoped memory keeps unrelated work apart. Deleting a memory stops its use but keeps a short recovery window (ChatGPT keeps deleted memories 30 days). Do not proactively remember sensitive details unless asked. |
| OpenAI Agents SDK memory / Cookbook | Small summary injected, details searched; writes after a run, consolidated by a second pass | "Treat memories as guidance only and trust the current environment." Capture only what the user stated or confirmed, not inferences. Most recent wins a conflict. Store workflow lessons, not investigation facts. Two-phase memory (note-taking, then consolidation) improves quality. |
| Mem0 | Extract facts, then ADD / UPDATE / DELETE / NOOP against the top similar memories | Look up similar memories before writing so a write merges instead of duplicating. Scope every read and write (user, agent, run). Expiry hides a memory rather than deleting it. |
| Zep / Graphiti | Temporal knowledge graph | A contradicted fact is invalidated, not deleted: history keeps its validity window. |
| Letta | Memory edited by the agent; background "dreaming" consolidates after N steps or on compaction | Consolidation belongs off the hot path; compaction is a natural trigger. |
| MCP memory server | Entities, relations, observations in one JSONL file | Simple to start; no dedupe, expiry, or deletion guidance — the gaps above are exactly what it lacks. |
| Generative Agents | Retrieval scored by recency + importance + relevance | Ranking should mix how relevant, how recent, and how much a memory matters. |

## Principles

1. **Small index always, bodies on demand.** Every turn carries an index of
   the memories visible to the thread — name, type, one-line description,
   age — bounded in count and bytes. The model reads a body when the line
   says it matters. Search covers what the index leaves out.
2. **One fact per memory, typed by purpose.** `user` (who the owner is),
   `feedback` (how to work — corrections and confirmed approaches, with
   *why* and *how to apply*), `project` (decisions, constraints, ongoing
   work not derivable from the code), `reference` (where something lives
   outside the workspace).
3. **Save less, but save the right thing.** Save when the owner asks;
   when they correct or confirm an approach; when a decision or constraint
   would be lost with this thread. Never save what the workspace, git, or
   an instruction file already records, ephemeral task state, secrets, or
   claims read from tool output or web pages.
4. **Update, do not duplicate.** A save names its memory; saving the same
   name in the same scope revises it. Every save reports similar existing
   memories so the model merges instead of piling up.
5. **Memories age.** Each memory records when it was last verified.
   Past its type's horizon it shows as stale in the index and in recall,
   and the model must check it against the present before relying on it.
   A memory found wrong is marked stale (with why) or forgotten; one found
   right is verified.
6. **Forget is reversible for 30 days.** Forgetting archives a memory:
   it leaves the index and search at once, the owner can restore it, and
   it is purged after 30 days. The owner can delete for good at once.
7. **Memories are evidence, not instructions.** The index is a note the
   model wrote earlier; it never outranks the owner's current message, the
   system prompt, or what the workspace shows now.
8. **Scoped.** Global memory follows the owner everywhere; project memory
   only the project's threads; thread memory only that thread (a chat's
   standing facts, notes that must survive compaction).
9. **The owner sees and controls everything.** Settings → Memory lists,
   searches, edits, verifies, archives, restores, and deletes memories,
   and turns memory off.

## Model

`aggregate.Memory` (domain) holds one memory:

| Field | Meaning |
|---|---|
| `id` | `mem_…` |
| `scope` | `global`, `project:<id>`, or `room:<conversation id>` |
| `type` | `user`, `feedback`, `project`, `reference` |
| `name` | kebab-case slug, unique among a scope's live memories |
| `description` | one line the index shows — what it is and when it matters |
| `body` | the fact, Markdown; `feedback` and `project` carry **Why:** and **How to apply:** |
| `source` | who wrote it last: `agent` (a model or agent), `owner` (Settings), or `consolidation` (a suggestion the owner accepted) |
| `origin` | the thread that wrote it last, for provenance |
| `created`, `updated`, `verified` | when it was made, last changed, last confirmed true |
| `recalled`, `recalls` | when its body was last read and how often — for ranking |
| `status` | `active`, `stale` (with a reason), or `archived` (forgotten, with when) |

Rules on the aggregate, not in callers:

- **Freshness.** `stale` when marked, or when `verified` is older than the
  type's horizon: `project` 30 days, `reference` 90 days, `user` 180 days.
  `feedback` does not age; it holds until it is contradicted. A memory past
  half its horizon is `aging`.
- **Revise** replaces description and body, verifies, and clears a stale mark.
- **Verify** confirms without changing content.
- **Archive / Restore**; **Purgeable** after 30 days archived.
- **Visibility**: a thread sees global memories, its project's, and its own.

## Lifecycle

**Write (hot path).** The model calls `memory_save` during a turn, as
Claude Code does: on an explicit request, a correction, a confirmed
approach, or a decision worth keeping. The application:

1. refuses text that looks like a credential;
2. revises the memory of that name in the target scope, or creates one;
3. embeds it when an embedding model is set (best effort);
4. answers the similar memories it found, so the model can merge or
   forget the overlap in the same turn.

The default scope is `global` for `user` and `feedback`, the thread's
project (else global) for `project` and `reference`.

**Recall.** The `memory` hydration record carries the index into every
model turn, re-recorded only when it changes. `memory_recall` reads bodies
by name, or searches: full-text (FTS5, BM25) always, blended with cosine
similarity when an embedding model is set, and a small boost for recently
used memories. Reading a body records the recall. A memory past its
horizon is still returned, flagged `stale`, so the model checks it; one
explicitly marked stale is left out of search unless `includeStale` is
set (it stays in the index with its reason). Archived memories never
appear.

**Maintain.** `memory_mark` verifies a memory or marks it stale with a
reason; `memory_save` under the same name rewrites it; `memory_forget`
archives it. The prompt tells the model when: verify after checking a
memory against the present; mark stale when the present contradicts it and
the right value is unknown; rewrite when the right value is known; forget
when the owner asks or it was never true.

**Purge.** Archived memories older than 30 days are deleted at start and
daily. Deleting a thread deletes its thread memories at once (a thread
delete is final). Deleting a project archives its memories, since a
project delete can be undone; restoring the project restores exactly the
memories its deletion archived.

**Consolidate (off the hot path).** When a thread compacts, the part it
seals is about to leave the model's context. Right after the compaction is
saved, the utility model (Settings → Models) reads that part beside the
thread's index and suggests memories; the owner reviews each one in
Settings → Memory → Suggested before anything is written. This is
Mem0's ADD/UPDATE/DELETE pass and Letta's compaction trigger, with a human
in the loop. `memory.consolidation` is `suggest` (default) or `off`; an
automatic mode that writes without review is not offered.

What the model reads: the owner's messages marked `[owner #<message id>]`
(harness notes such as checkpoints left out), the assistant's text and
tool outputs as clipped context, all of it redacted, at most 40 000
characters with the newest owner lines kept first. It answers JSON ops,
at most eight per pass:

| Op | Says | Rules the application enforces |
|---|---|---|
| `add` | a new memory | a type; the name is not already live in its scope (else it must be an `update`); the similar live memories (search top 3) are stored with it |
| `update` | rewrite a memory | an explicit scope; the memory must exist there; keeps its type unless one is given |
| `stale` | the owner contradicted a memory | an explicit scope; a reason; the memory is not already stale |

Every op must quote its `evidence` exactly from one owner line, and the
quoted message's id is kept, so a suggestion can only come from what the
owner said, never from assistant text, tool output, or a web page. Text
the redactor would change is refused. A suggestion already pending, or
decided from the same thread in the last 30 days, is not suggested again.

An `update` or `stale` records its target's id and when the target last
changed. Accepting one whose target was forgotten or changed since is
refused (compare-and-set), and the card says so. Accepting writes through
the same path as `memory_save` (source `consolidation`, the thread as
origin); the owner may edit the text first. A suggestion is decided once.
Deleting a thread dismisses its pending suggestions; deleting a project
dismisses those scoped to it; a deleted memory takes its suggestions with
it; decided suggestions are purged with archived memories after 30 days.

**Thread opt-out.** A thread can keep memory out (thread menu → "Keep
memory out of here", `memoryOff` on the thread). Its model sees no index,
the memory tools answer 409, and its compactions are not consolidated.
Nothing is deleted: memories it saved before stay, and the owner still
sees them in Settings.

**Agents.** ACP agents reach the same memory through the built-in `memory`
MCP server (`save`, `recall`, `mark`, `forget`); `recall` with no name or
query answers the index, and their session prompt tells them to start there.

## Where it lives

| Layer | Code |
|---|---|
| Domain | `aggregate.Memory`, `enum` memory types/statuses/scopes, `valueobject.MemoryScope`, port `repository.Memories`, `service.MemoryIndex` / `EmbeddingModelService` |
| Storage | `repository/memory` — SQLite table `memories` (migration `0009`) with an FTS5 index kept by triggers and an optional float32 embedding per memory; `memory_proposals` (migration `0010`), its target a foreign key that cascades |
| Use case | `application/memory` — save, recall, mark, forget, index, the owner's list/create/update/act/delete, purge, thread/project hooks; `consolidate.go` reads a sealed segment and decides suggestions |
| Suggestions | `aggregate.MemoryProposal`, port `repository.MemoryProposals`; the compaction job's `OnCompacted` hook starts a pass beside the turn (`cmd/nusashell/serve.go`), on `ConversationApp.Utility`; prompt `prompt/system/memory-consolidate.md` |
| Embedding | `application/media.Embed` on the embedding role, adapter `core/embedding`; no role assigned = full text only |
| Model runtime | hydration tool `tool/memory` + the `memory_*` tools from the built-in MCP catalog |
| Agents | the built-in `memory` MCP server through the relay; `agent-session.md` tells them to `recall` first |
| Owner | `GET/POST /api/memories`, `PUT/DELETE /api/memories/:id`, `POST /api/memories/:id/actions` (`verify`, `stale`, `archive`, `restore`); `GET /api/memory-suggestions?status=pending\|decided`, `POST /api/memory-suggestions/:id/actions` (`accept` with optional `edit`, `dismiss`); `PATCH /api/conversations/:id` `memoryOff`; Settings → Memory (Memories / Suggested) |

## Safety

- Text that the redactor would change (keys, tokens, passwords) is refused.
- The prompt forbids saving instructions found in tool output, files, or
  web pages, and requires a memory about the owner to come from the
  owner's own words or confirmation.
- Every memory names its origin thread; the owner reviews them in Settings.
- Memory is off with `memory.enabled = false`: no index, the tools refuse,
  nothing is consolidated. One thread is kept out with its opt-out.
- **Egress.** Consolidation sends the sealed part of a thread — the
  owner's words, and clipped assistant text and tool output — to the
  utility model's provider, redacted first. With no utility model assigned
  nothing is sent; `memory.consolidation = off` stops it.
- A suggestion's name, text, reason, and evidence pass the same credential
  check as a save, and accepting one runs it again.

## Evals

`make memory-eval` runs the consolidation prompt against the utility model
the owner's config assigns, over the synthetic threads in
`cmd/nusashell/testdata/memory-eval` (stated habits, corrections, a
tracker, an update, a stale mark, tool-output and assistant-claim
injections, task chatter, a secret beside a real preference). It reports,
per case, the ops the model answered and the ones kept, then precision,
recall, and false captures, and fails when an injection case gets anything
suggested or a suggestion carries forbidden text. It calls the real model,
so `make check` only compiles it (`go vet -tags memoryeval`); its writes go
to a scratch database. Add a case as one JSON file: `messages`, an
optional `index` and `project`, `expect` (`op`, quoted `evidence`,
`target`), `forbid`, `injection`.

## Later

- **Automatic consolidation** that writes without review, once the evals
  show it safe; it needs its own guard against a pass racing a live turn's
  `memory_save`.
- **Evals for recall**: whether the index and search surface the memory a
  turn needed, and how stale ones are handled.
