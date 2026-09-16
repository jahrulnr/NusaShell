# Announcement queue

A per-conversation queue that delivers prompt-cache-breaking state changes
(subagent config, memory, skills, settings, providers) to agent conversations
as `announcement` tool calls. The goal is token efficiency: the change already
breaks the prompt cache silently; the announcement makes the breakage visible
to the model so it does not waste tokens re-discovering the change or acting
on stale assumptions.

## Problem

The system prompt prefix, tool definitions, and the hydration checkpoint are
deliberately cache-stable. Several runtime changes invalidate that prefix
without the model being told:

- **ACP subagent save/delete/enable** — rewrites the `subagent` tool
  description (`AcpDelegationDescription`), breaking the cached tool block
  for every conversation.
- **Settings.UserPrompt** — appended to the system prompt as
  `<user_instructions>`, breaking the cached system block globally.
- **Memory / skills changes** — alter hydration slot content; mid-epoch
  changes would otherwise stay invisible to the model until compaction.
- **Provider/model changes** — change the cache key (provider+model+conversation),
  forcing a fresh shard.

Those surfaces now announce through the queue below (`config_changed`,
`memory_changed`, `skills_changed`) in addition to `restart`,
`auto_continue`, `interrupted`, and `workspace_changed`
(`domain/announcement.go`).

## Delivery semantics

The announcement is a plain notice — a type (coalescing key), self-describing
tool args, and an implicit result text. It is deliberately NOT a message bus:
no routing keys, no delivery metadata, no consumers. The persisted
per-conversation pending queue IS the queue:

1. **Publish = Send.** `publishAnnouncement` appends to
   `Conversation.PendingAnnouncements` via `QueueAnnouncement`, which
   deduplicates exact-content duplicates (same `type`, `args`, and
   `message`) and appends distinct notices in arrival order (cap 64;
   oldest dropped on overflow). A burst publishing the same notice
   collapses into one entry; distinct notices of the same type are kept.
   Fail-soft: a missing conversation only logs; the change is
   self-healing at the next hydration epoch.
2. **Drain = Receive.** `drainAnnouncements` injects every pending entry as
   a persisted assistant message (pre-filled `announcement` tool result) and
   clears the queue. The turn lock guarantees a single consumer per
   conversation; the per-conversation announcement lock serializes
   load-modify-save against concurrent publishers, so entries are never lost
   or double-injected.
3. **Delivery points.** Turn start (`addTurnMessages`, after the user
   message — the task-memory scan runs here before the drain), tool-round
   boundary (`AfterRound`, alongside steer and subagent results), and an
   event-driven idle wake for `peer_message`. Active turns never have their
   provider request or tool call cancelled. An idle conversation with real
   user history gets one new `TurnRun` after the announcement is materialized;
   an empty draft is not a durable target and peer delivery is rejected.
4. **Durable.** The queue is persisted on the conversation — survives
   backend restarts and arbitrarily long idle periods. No TTL, no in-memory
   retention, no sweeper: the queue is drained or it stays.

## Architecture

```
publisher (RPC handler / Skills UI / About You·Agent save / cross-conversation tool path)
   │  publishAnnouncement(convID, ev): lock → append (coalesce) → save
   ▼
Conversation.PendingAnnouncements (persisted, per-conversation queue)
   │  drained at turn start, round boundary, or idle peer-message wake
   ▼
persist announcement message (assistant + pre-filled tool result)
   │  clear the queue
   ▼
next provider round reads it from the transcript
```

## Queue API

`application/agent/announcement.go` (stdlib only):

```go
type Announcement struct {
    Type    string // config_changed | memory_changed | skills_changed | peer_message | task_memory
    Args    string // self-describing JSON args (announcement tool args)
    Message string // announcement tool result text
}

func (a *App) publishAnnouncement(convID string, ev Announcement) // Send
func (a *App) publishAnnouncementToAll(ev Announcement, skipConvID string)
func (a *App) drainAnnouncements(run *TurnRun) (bool, error)      // Receive
```

- `publishAnnouncement` takes the per-conversation announcement lock around
  load-modify-save, appends via `QueueAnnouncement` (exact-content dedup;
  distinct notices append, cap 64), and saves.
- `drainAnnouncements` takes the same lock, injects all pending entries as
  assistant messages, clears the queue, and saves. Returns true when
  something was injected, forcing the round to continue.
- The existing `Bus` (`application/bus.go`) is NOT reused: it is the UI/SSE
  stream with no per-conversation delivery guarantees. The queue is a
  separate, internal-only path.

## Event catalog

New announcement types on the existing `announcement` tool channel, following
the self-describing args pattern of `AutoContinueAnnouncementArgs`:

| Type | Publishers | Args | Result text (implicit, model re-reads details) |
|------|-----------|------|------------------------------------------------|
| `config_changed` | `acp.agents.save/delete`, `settings.save` (UserPrompt), `ai.providers.save/delete` | `{type, changed: ["subagent codex has disabled"]}` | concrete named status/change, e.g. `subagent codex has disabled; subagent devin has enabled. Re-read the affected tool descriptions and instructions.` |
| `memory_changed` | `memory.user.update` / `memory.agent.update` RPC | `{type, tier: "user"\|"agent", op: "update"}` | `user.md has changed, read /absolute/path/memory/user.md to see primary memory` (or `soul.md`) |
| `skills_changed` | `skills.save` / `install` / `delete` RPC, `skill` tool (`save`/`delete`) from other conversations | `{type, op, name}` | `skill <name> has changed, re-read if you are using this skill` |
| `peer_message` | `conversation(op="send")` | `{type, from}` | quoted inter-room message plus reply guidance |
| `task_memory` | turn-start scan (`addTurnMessages` → `MaybeAnnounceTaskMemory`); async semantic lane (`prefetchTaskMemorySemantic` → `PublishTaskMemoryAnnouncement`) | `{type, hits: [{id, type, project, content}]}` | "Relevant task memory for this conversation is new or updated. Read the snippets; retrieve full records with memory op=search or memory op=get." |

Rules:

- **Targeted content.** Configuration announcements include the changed
  surface/name/status, memory announcements include the primary document path,
  and skill announcements include the changed skill name. None of them dumps
  the full configuration, memory body, or skill body; the model re-reads those
  details from the live surfaces.
- **No self-announcement.** When the agent itself calls `skill` tools in
  this conversation, no event is published — the model already knows
  (it made the call). Only external mutations announce: UI RPC, other
  conversations. The `memory` tool is read-only (`search`/`get`/`list`);
  it does not publish `memory_changed`.
- **Fan-out.** Profile-document updates and skill-library writes publish
  to every visible conversation. Idle conversations get the pending entry
  too (drained at next turn start). Consolidator jobs emit `memory.updated`
  for the Learning UI; they do not enqueue `memory_changed` announcements.
- **Peer delivery.** `conversation(op="send")` acknowledges only after the
  target queue is persisted. If the target is active, the existing round
  boundary consumes it. If the target is idle and has real user history, the
  harness starts one turn; this wake path does not add a synthetic user
  message. Provider resolution failure leaves the queue durable.

## Worker lifecycle

- **No subscription.** Active delivery is the turn's round loop; idle peer
  delivery is an explicit wake call from the publisher. There are no polling
  channels, unsubscribe paths, or mailbox goroutines.
- **Drain**: `drainAnnouncements` in `AfterRound`
  (`application/agent/conversation_agent_rules.go`), alongside `applyQueuedSteer` /
  `applyQueuedRunResults`. Injected announcements append a persisted
  assistant message (pattern: `autoContinueAnnouncement` in
  `agent_turn_run.go`) and force the round to continue so the model sees
  them. This is the "after tool-call" delivery point.
- **Turn start**: `addTurnMessages` drains `Conversation.PendingAnnouncements`
  after the user message (and after the workspace notice / restart
  announcement). This is the "every user message" delivery point.
- **Idle peer wake**: `conversation(op="send")` persists the queue, then the
  agent service checks the target lifecycle. When idle with user history it
  appends the announcement and assistant placeholder, marks the conversation
  running, registers one `TurnRun`, and launches it. Cleanup re-checks the
  queue to close the race where a peer message arrives while the previous
  turn is sealing.
- **Concurrency**: the turn lock guarantees one consumer per conversation.
  The per-conversation announcement lock (`announcementLock` in
  `application/agent/announcement.go`) serializes publisher load-modify-save
  against the worker drain, so a publish racing a drain is never lost and
  never double-injected.

## Persistence

- `domain.Conversation` carries `PendingAnnouncements []PendingAnnouncement`
  (the workspace flag stays as is). Each entry:
  `{id, type, args, message, created_at}`.
- Publish appends the pending entry and saves (single commit point in the
  handler, after the store write succeeds).
- Announcements injected into the transcript are persisted like restart /
  workspace announcements. They are stripped at compaction — the fresh
  hydration checkpoint already carries the current memory/skills/MCP state,
  so stale announcements add no value after an epoch boundary.

## Publisher inventory

| Site | Event |
|------|-------|
| `HandleAgentsSave` / `HandleAgentsDelete` (`application/subagent/handlers.go`) | `config_changed` with subagent name + enabled/disabled/deleted/changed action |
| `onSettingsApplied` (`application/feature_services.go`) when `UserPrompt` changed | `config_changed` (user instructions) |
| `HandleSave` / `HandleDelete` (`application/provider/handlers.go`) | `config_changed` with provider name + action |
| `HandleUserUpdate` / `HandleAgentUpdate` (`application/memory/handlers.go`) | `memory_changed` with `user.md`/`soul.md` path |
| `HandleSave` / `HandleInstall` / `HandleDelete` (`application/skills/handlers.go`) | `skills_changed` with skill name |
| `skill` tool `save`/`delete` when the calling conversation differs from the affected one | `skills_changed` with skill name |
| `MaybeAnnounceTaskMemory` (`application/memory/task.go`) at turn start | `task_memory` |
| `prefetchTaskMemorySemantic` async lane (`application/task_memory_semantic.go`) → `PublishTaskMemoryAnnouncement` | `task_memory` |

## Test plan

- Queue unit tests: publish appends + deduplicates exact-content
  duplicates (same `type`, `args`, and `message`), drops the oldest entry
  past the cap; drain injects all pending types in order and clears the
  queue; empty queue is a no-op.
- Concurrency test: parallel publishers + drain never lose or double-inject
  (per-conversation lock).
- Turn-start tests: `addTurnMessages` drains pending announcements in order
  (user → workspace → pending → restart → assistant); idle-then-message
  delivery; restart survival.
- Publisher tests: each handler publishes the right event only on real
  change (e.g. UserPrompt unchanged → no event); profile-document fan-out
  to all conversations; hidden/self conversations skipped.
- Compaction test: announcements are stripped; fresh hydration carries the
  state.
- Regression: `TestAppendContinuationTool` and the system-prompt stability
  suite stay green — announcements never touch the system prompt.
