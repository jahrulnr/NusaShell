# Agent conversations: storage I/O design (audit + best practices)

Status: analysis/DRAFT — for review, not yet implemented or committed.

## Current design (one file, full rewrite per delta)

- `AgentConversationStore` persists every conversation into a **single file**
  `agent-conversations.json` under `userData`.
- Every mutation (`appendMessage`, `saveCheckpoint`, `setWorkspace`, …) goes
  through a serialized `mutate()` queue and calls `persist()`, which
  **rewrites the entire document** (`JSON.stringify(state)` → `path.tmp` →
  atomic `rename`).
- `this.state` is a memory cache updated only after a successful persist.

### Consequences at scale (the problem)

| Property | Behavior |
| --- | --- |
| Write cost per delta | `O(total data)` — every append rewrites all conversations |
| Concurrency | `mutate()` serializes all writes (one process) |
| Many agents | parallel turns queue up behind one big rewrite each |
| Single point of growth | all rooms share one file; one large room makes every write slow |

Data today is small (repo `.nusashell/agent-conversations.json` ≈ 110 KB, 1 conv,
2 msgs). The issue is a **scaling projection**, not a current bug: at large
payload (attachments, long transcripts) + multiple parallel agents, per-delta
full rewrite and serialized writes add real latency.

## Design goals

- Keep results as **JSON that can be built into the full payload for the API**
  (one conversation → one document).
- Write cost should be **O(1) per delta for the common case (append)**, not
  O(total).
- Race/gagal-tulis safe: crash mid-write must not lose committed deltas.
- No SQLite (user preference: "cuma mindahin masalah"), no over-engineered
  external store.

## Candidate approaches (ranked by fit)

### A. Snapshot + per-conversation append log (recommended)

Split into **one file per conversation**. Inside each file store:

- A **snapshot** (the materialized conversation document) — this is what the
  API build reads directly (no full replay).
- A short **append-only tail log** of deltas since the last snapshot.

Writes:
- `appendMessage` → append one record to the tail log = **O(1)** (atomic
  `O_APPEND`, record <~4 KiB).
- Snapshot rewritten when the tail grows past N (compaction), or on idle —
  batch the snapshot rebuild, not every delta.

Reads / API build:
- Load snapshot, replay tail (`snapshot + delta`), produce the full document.
  Because the tail is bounded by the compaction cadence, replay is cheap.

Safety:
- Each delta carries a **monotonic seq + idempotent apply** (dedup by message
  id) so a crash between snapshot and tail, or a retried append, cannot
  duplicate or drop a committed message.
- Separating files removes cross-conversation write contention entirely.

Trade-offs: medium-to-large refactor; needs migration of the existing single
file → per-conversation files; new compaction scheduler; test rework.

### B. Per-conversation files, keep full-materialize-per-file (smaller)

One file per conversation, each still rewritten wholesale on its own mutation.
- Solves the **cross-conversation** contention (room A writes no longer touch
  room B).
- Does NOT solve per-conversation O(total) within a large room.

Recommended only if the dominant cost is many parallel rooms with modest size.

### C. Keep single file + bounded debounce (smallest)

Keep one file; coalesce deltas into a periodic flush (~150 ms idle drain).
- Cuts IO frequency, keeps format.
- Still O(total) per flush; still one shared file.
- Race risk guarded by the existing serialized `mutate()` queue (append is
  already ordered), but flush timing complicates crash guarantees.

## Recommendation

Proceed with **A (per-conversation snapshot + append log)** as the
best-practice target, staged behind the current store contract so the renderer
(`shell.agentConversations.*`) does not change. Implementation order:

1. Define the on-disk format + seq/idempotency + compaction threshold.
2. Migrate the single file → per-conversation files (backward-compatible read).
3. Add append + snapshot + replay primitives with tests (idempotent replay,
   crash-injection, concurrent appends).
4. Wire compaction (idle + tail threshold).
5. Full verify (frontend + backend + typecheck + scan-ui-docs).

Not committed — awaiting review.

## Implementation status (DONE, TDD — not committed)

Adopted **option = per-conversation 2-file layout + per-conversation locks**,
mirroring Codex's split (rollout JSONL for messages, metadata/thread stored
separately). No SQLite.

### Final on-disk format
Given constructor `path` (was `<userData>/agent-conversations.json`):

```
dirname(path)/conversations/
  <id>.jsonl           # message history, one AgentConversationMessage per line
  <id>.meta.json       # id, title, createdAt, updatedAt, messageCount, kind?, acp?,
                       #   workspace?, model?, checkpoint?, activeCanvas?, activeSubagent?
  <id>.artifacts.json  # canvas artifacts (only when non-empty)
  <id>.subagents.json  # subagent runs (only when non-empty)
```

Legacy single-file is migrated once to the new layout, then renamed to
`path.migrated`. A corrupt monofile still throws (no silent rewrite).

### Mechanics (mirroring Codex)
- **Per-conversation locks** — `Map<convId, Promise>` (`withLock`); same-room
  ops serialized, different rooms run in parallel. `LIST_LOCK` / `LEGACY_LOCK`
  guard list() and migration.
- **Append** — `appendMessage` = O(1) `appendJsonlLine` + small atomic meta
  update (`messageCount`, `title`, `updatedAt`).
- **Trim** — after append, if JSONL `> maxBytes` (default 8 MiB; settable for
  tests), drop oldest down to ~0.8× cap (Codex soft-cap ratio), rewrite JSONL.
- **Read** — `get` = meta + JSONL + side files; `list` = meta only +
  `messageCount`, newest `updatedAt` first.
- **Replace tail** — `replaceLastInterrupted` reads/drops-last/appends/rewrites
  JSONL (rare path, not hot).
- **Atomic** — meta/side files via tmp+rename.

### Verification (all green)
- `agent-conversation-store.test.ts`: **30** (was 22) — added layout persistence,
  lock concurrency (same-room serialized, cross-room parallel), long-conversation
  build, trim, delete cleanup, replace-tail.
- `agent-conversation-jsonl.test.ts`: **4** (append order, one-record-per-line w/
  escaped newlines, missing file, torn-tail crash safety).
- Desktop full suite: **64 files / 473 tests** green.
- Backend full suite: **150 files / 1606 tests** green (unaffected).
- `scan-ui-docs` exit 0; `pnpm typecheck` exit 0.

Files changed: `src/main/agent-conversation-store.ts` (rewrite), 
`tests/agent-conversation-store.test.ts` (+8), new `src/main/agent-conversation-jsonl.js`
(primitives) + `.d.ts`. Not committed (user reviews).
