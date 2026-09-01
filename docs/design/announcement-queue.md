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
  changes are currently invisible to the model until compaction.
- **Provider/model changes** — change the cache key (provider+model+conversation),
  forcing a fresh shard.

Today only `restart`, `auto_continue`, `interrupted`, and `workspace_changed`
are announced (`domain/announcement.go`). The rest change the request payload
silently.

## Delivery semantics

The announcement is a plain notice — a type (coalescing key), self-describing
tool args, and an implicit result text. It is deliberately NOT a message bus:
no routing keys, no delivery metadata, no consumers. The persisted
per-conversation pending queue IS the queue:

1. **Publish = Send.** `publishAnnouncement` appends to
   `Conversation.PendingAnnouncements` (coalesced by type — a burst of
   changes collapses into one announcement) and saves. Fail-soft: a missing
   conversation only logs; the change is self-healing at the next hydration
   epoch.
2. **Drain = Receive.** `drainAnnouncements` injects every pending entry as
   a persisted assistant message (pre-filled `announcement` tool result) and
   clears the queue. The turn lock guarantees a single consumer per
   conversation; the per-conversation announcement lock serializes
   load-modify-save against concurrent publishers, so entries are never lost
   or double-injected.
3. **Delivery points.** Turn start (`addTurnMessages`, after the user
   message) and tool-round boundary (`AfterRound`, alongside steer and
   subagent results). Both are safe injection points; the model sees the
   announcement in the next provider round.
4. **Durable.** The queue is persisted on the conversation — survives
   backend restarts and arbitrarily long idle periods. No TTL, no in-memory
   retention, no sweeper: the queue is drained or it stays.

## Architecture

```
publisher (RPC handler / review agent / cross-conversation tool path)
   │  publishAnnouncement(convID, ev): lock → append (coalesce) → save
   ▼
Conversation.PendingAnnouncements (persisted, per-conversation queue)
   │  drained at turn start (addTurnMessages) and round boundary (AfterRound)
   ▼
persist announcement message (assistant + pre-filled tool result)
   │  clear the queue
   ▼
next provider round reads it from the transcript
```

## Queue API

`application/announcement.go` (stdlib only):

```go
type Announcement struct {
    Type    string // "config_changed" | "memory_changed" | "skills_changed"
    Args    string // self-describing JSON args (announcement tool args)
    Message string // announcement tool result text
}

func (a *App) publishAnnouncement(convID string, ev Announcement) // Send
func (a *App) publishAnnouncementToAll(ev Announcement, skipConvID string)
func (a *App) drainAnnouncements(run *TurnRun) (bool, error)      // Receive
```

- `publishAnnouncement` takes the per-conversation announcement lock around
  load-modify-save, appends via `QueueAnnouncement` (coalescing by type,
  latest wins), and saves.
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
| `config_changed` | `acp.agents.save/delete`, `settings.save` (UserPrompt), `ai.providers.save` | `{type, changed: ["subagent","user_prompt"]}` | "Tool/system configuration changed since your last turn: subagent list, user instructions. Re-read the affected tool descriptions and instructions." |
| `memory_changed` | `memory.save`/`memory.delete` RPC, review agent, `memory` tool from other conversations | `{type, tier: "primary"\|"fragment", op: "save"\|"replace"\|"delete"}` | "Memory was updated outside this conversation. Call `memory` op=list to refresh." |
| `skills_changed` | `skills.save/install/delete` RPC, `skill` tool from other conversations | `{type, op}` | "The skill library changed. Call `skill` op=list to refresh." |

Rules:

- **Implicit content.** The announcement never dumps the new content — the
  model already receives the new system prompt / tool descriptions in the
  request. It only flags the change and points at the refresh tool. Example
  (settings): "User instruction from system prompt has changed: ..." — the
  model sees the new instruction in the same request.
- **No self-announcement.** When the agent itself calls `memory`/`skill`
  tools in this conversation, no event is published — the model already knows
  (it made the call). Only external mutations announce: UI RPC, the
  background review agent, other conversations.
- **Review agent → all conversations.** A review-agent memory write
  publishes to every visible conversation (fan-out). Idle conversations get
  the pending entry too (drained at next turn start).

## Worker lifecycle

- **No subscription.** The worker is the turn's round loop; it drains the
  persisted queue at round boundaries — no channels, no unsubscribe, no
  in-memory state to leak.
- **Drain**: `drainAnnouncements` in `AfterRound`
  (`application/conversation_agent_rules.go`), alongside `applyQueuedSteer` /
  `applyQueuedRunResults`. Injected announcements append a persisted
  assistant message (pattern: `autoContinueAnnouncement` in
  `agent_turn_run.go`) and force the round to continue so the model sees
  them. This is the "after tool-call" delivery point.
- **Turn start**: `addTurnMessages` drains `Conversation.PendingAnnouncements`
  after the user message (and after the workspace notice / restart
  announcement). This is the "every user message" delivery point.
- **Concurrency**: the turn lock guarantees one consumer per conversation.
  The per-conversation announcement lock (`announcementLock` in
  `application/announcement.go`) serializes publisher load-modify-save
  against the worker drain, so a publish racing a drain is never lost and
  never double-injected.

## Persistence

- `domain.Conversation` gains `PendingAnnouncements []PendingAnnouncement`
  (generalizing `PendingWorkspaceAnnouncement`; the workspace flag stays as
  is). Each entry: `{id, type, args, message, created_at}`.
- Publish appends the pending entry and saves (single commit point in the
  handler, after the store write succeeds).
- Announcements injected into the transcript are persisted like restart /
  workspace announcements. They are stripped at compaction — the fresh
  hydration checkpoint already carries the current memory/skills/MCP state,
  so stale announcements add no value after an epoch boundary.

## Publisher inventory

| Site | Event |
|------|-------|
| `handleAcpAgentsSave` / `handleAcpAgentsDelete` (`application/acp_handlers.go`) | `config_changed` (subagent) |
| `handleSettingsSave` (`application/settings_handlers.go`) when `UserPrompt` changed | `config_changed` (user_prompt) |
| `handleProvidersSave` / `handleProvidersDelete` (`application/providers.go`) | `config_changed` (provider) |
| `handleMemorySave` / `handleMemoryDelete` (`application/memory_handlers.go`) | `memory_changed` |
| Review agent memory/skill writes (`application/review_agent_rules.go`) | `memory_changed` / `skills_changed` (fan-out) |
| `handleSkillsSave` / `handleSkillsInstall` / `handleSkillsDelete` (`application/skills_handlers.go`) | `skills_changed` |
| `memory` / `skill` tool execution when the calling conversation differs from the affected one | `memory_changed` / `skills_changed` |

## Test plan

- Queue unit tests: publish appends + coalesces by type (latest wins);
  drain injects all pending types in order and clears the queue; empty queue
  is a no-op.
- Concurrency test: parallel publishers + drain never lose or double-inject
  (per-conversation lock).
- Turn-start tests: `addTurnMessages` drains pending announcements in order
  (user → workspace → pending → restart → assistant); idle-then-message
  delivery; restart survival.
- Publisher tests: each handler publishes the right event only on real
  change (e.g. UserPrompt unchanged → no event); review agent fan-out to all
  conversations; hidden/self conversations skipped.
- Compaction test: announcements are stripped; fresh hydration carries the
  state.
- Regression: `TestAppendContinuationTool` and the system-prompt stability
  suite stay green — announcements never touch the system prompt.
