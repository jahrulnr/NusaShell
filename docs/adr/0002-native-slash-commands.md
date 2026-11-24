# ADR-0002: Native slash commands in the composer

## Status

Accepted — binding contract for the SA/BE/FE/QA work split.

## Context

The composer already resolves `@` (threads, projects), `/` (agent commands, files,
folders, skills, MCP servers), and uploads. Those `/` entries are **references**:
picking one inserts text and a byte-range selection that the turn sends to the
model or the agent. There is no NusaShell-owned command: `/compact` exists only as
a header-menu action (`POST /api/conversations/{id}/compactions`), and "clear
history" and "fork this thread" exist only as *delete the room* and *start another
room*.

Manual compaction is refused for agent rooms (`409 agent conversations manage
their own context`). Rooms, messages, and tool output are stored per conversation
directory; a room's live tail is what the model sees, sealed segments are its
archive.

## Decision

Add three native commands — `compact`, `clear`, `fork` — owned by the application
layer and exposed through one catalog and one execution endpoint. A native
command is an **action, not a turn**: it creates no message and no `turn.*` frame.

### Recognition

- A command is the message's first line: optional leading whitespace, then
  `/` + name, matched **case-sensitively** against the catalog, then optional
  arguments separated by whitespace.
- A name in the catalog always dispatches as a command; a name outside it stays
  ordinary text (a path such as `/home/x`, an agent command, prose). A recognized
  command never reaches a model or an agent, even when unavailable or refused.
- The client may insert the command as plain text; the dispatch decision is made
  when the message is submitted, from the drafted text, not from the reference
  selections. Command names are therefore reserved words in line 1.

### Catalog

`GET /api/commands?conversationId=<id>` is the only command read. It returns the
native catalog merged with the room's agent commands, native first:

```json
{ "data": [
  { "name": "compact", "description": "Summarize older history into a checkpoint",
    "hint": "", "source": "native", "enabled": true, "reason": "" },
  { "name": "review", "description": "Review the working tree", "hint": "/review [path]",
    "source": "agent", "enabled": true, "reason": "" }
] }
```

- `source` is `native` or `agent`. An agent command whose name collides with a
  native one is **omitted** — native wins, one row per name.
- `enabled`/`reason` are the server's verdict for this room; an agent command is
  always `enabled: true` (the agent decides), a native one follows the matrix below.
- Without `conversationId`: native only, every row `enabled: false`,
  `reason: "Send a message first"`.
- `GET /api/conversations/{id}/commands` is removed; the client moves to the
  catalog above.

### Execution

`POST /api/conversations/{id}/commands` with `{ "name", "args", "confirm" }`.

- `200` → `{ "command": "<name>", "conversation": <room detail projection> }`.
  For `fork` the projection is the **new** room.
- `400 invalid_argument` — unknown name, unsupported arguments.
- `400 nothing_to_compact` / `nothing_to_clear` / `nothing_to_fork`.
- `409 confirmation_required` — a destructive command called without `confirm`.
- `409 busy` — a transport turn is live in the room.
- `409 unavailable` — the command does not apply to this room kind.

The application, not the transport, enforces every precondition, so a future
channel ingress (`/clear confirm` typed in WhatsApp) reuses the same path without
new rules.

### Invariants

1. Native commands run only in an **idle** room: a live `turn.send`/`turn.edit`
   refuses every command with `409 busy`. Compaction, clearing, and copying
   history under a running turn are all unsound.
2. A command never writes a message. `/clear` and `/fork` leave no trace in the
   transcript; `/compact` writes only the checkpoint the existing compaction writes.
3. A refused or unavailable command keeps the client's draft; only a `200`
   clears it.
4. `compact` keeps today's meaning exactly: provider rooms only, `canCompact`
   required, the same checkpoint dialogue on the room worker. Agent rooms report
   `enabled: false` with `reason: "The agent manages its own context"`.
5. `clear` empties one room without changing its identity: every message, sealed
   segment, and conversation-owned tool-output file is removed; the room record
   keeps id, title, provider/model/effort/upstream (or agent binding and its
   options), workspace, environment, project, `@label`, reply routing, pin,
   memory setting, and accumulated usage. The agent session and agent-context
   record are dropped, so the next turn starts a fresh one.
6. `clear` requires `confirm: true`. The web UI asks with a danger dialog; a
   chat ingress must send the word `confirm` itself.
7. `fork` creates a new room that is a full copy of the source's logical history
   (sealed segments plus live tail, with each message's attachments and
   tool-output files) and of its bindings, workspace, environment, project,
   memory setting, and agent session options. It copies **no** `@label`, reply
   routing, pin, approval rules, agent session handle, usage (starts at zero), or
   saved view. Title is `<source title> (fork)`; a source still on its default
   title keeps the default and auto-titles on its first send. The source is never
   modified.
8. A forked room becomes visible only once its history is complete; another
   reader must never find a half-copied thread.
9. `/compact` and `/fork` take no arguments in this version; `/clear` accepts only
   `confirm`. Extra tokens are `400 invalid_argument`. Recognized commands with
   wrong arguments are refused, never downgraded to text.

### Availability matrix

| Condition | compact | clear | fork |
|---|---|---|---|
| Fresh composer (no room) | off: Send a message first | off: same | off: same |
| Live turn | off: Wait for the turn to finish | off: same | off: same |
| Agent room | off: The agent manages its own context | on | on |
| Empty room | off: Nothing to compact yet | off: This thread is empty | off: Nothing to fork yet |
| No new foldable history | off: Nothing to compact yet | — | — |

The composer renders a disabled row with its reason and refuses to run it on
Enter; it never falls back to sending the text.

## Consequences

### Positive

- One definition of each command for the web UI, chat channels, and automations;
  the future channel ingress adds no second rule set.
- History operations (compact, clear, fork) become named, testable use cases
  instead of ad-hoc HTTP verbs.
- The `/` menu stays the single discoverability surface: commands, files, skills,
  and MCP servers in one list, each with its source and availability.

### Negative

- Breaking change: `POST /api/conversations/{id}/compactions` and
  `GET /api/conversations/{id}/commands` go away; the header button and the
  composer both call the command endpoint.
- Names in the catalog are reserved: a room cannot send a first-line `/compact`
  as prose to its model. Documented in the composer hint.
- `fork` duplicates history and tool-output files on disk; storage grows with
  each fork, and deleting the fork later is the owner's cleanup.

## Non-goals

Channel/automation ingress, per-message forks, command aliases, user-defined
commands, `/help`, and agent-command namespacing beyond native precedence.
