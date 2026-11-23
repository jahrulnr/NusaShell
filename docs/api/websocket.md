# WebSocket protocol — turn transport

**Status: implemented.** All six commands are live: `turn.send` and
`turn.edit`/`turn.resume` open server-scoped turns on the hub's own context;
`turn.steer` submits steering input into an exact send turn's mailbox
and `turn.steer_recall` withdraws one still-pending input;
`turn.cancel` cancels an exact live turn by `turnId` (never the
room-wide worker interrupt); `turn.watch` replays the bounded journal
under the hub lock and attaches for live frames, answering
`turn.resync_required` when the journal can't satisfy the window. A
`conversation.activity` notice is broadcast to every socket when a
room gains or loses a live turn. The
OpenAPI spec in [`openapi.yaml`](./openapi.yaml) covers only the HTTP
upgrade — it does not define frames.

## Connection

- **Endpoint:** `GET /api/ws`, upgraded to
  `ws://127.0.0.1:<port>/api/ws` or another configured `server.hosts`
  alias. The default port is `10994`; default aliases are `127.0.0.1`
  and `localhost`.
- **Security:** the handshake must satisfy the same rules as unsafe HTTP
  methods — `Host` and `Origin` must match a configured host alias with
  the configured port. Violations are rejected `403` *before* the socket
  opens; there is no in-band auth and no token.
- **One user, many tabs:** the server accepts multiple concurrent
  sockets (the browser may hold several tabs). Turn events are delivered
  to the socket that owns the turn plus any socket that has attached via
  `turn.watch` — turn streams are never broadcast room-wide. The one
  room-wide notice is `conversation.activity`: an unsequenced,
  unjournaled frame sent to every socket when a room gains or loses a
  live turn, so a second tab discovers the activity (a new room, an
  attachable turn, a settled room) without re-opening it. It still does
  not passively observe the turn's stream; it sees persisted state
  through HTTP reads.
- **Reloading a room.** `GET /api/conversations/{id}` and each item of
  `GET /api/conversations` include
  `activeTurnId` and `activeTurnKind` (`send` or `edit`) while this
  process has a non-terminal transport turn for that room. A client that
  loads or selects the room attaches with `turn.watch` and `afterSeq: -1`,
  then re-reads HTTP on the terminal frame. The fields are absent when
  the room is idle. A journal gap still answers `turn.resync_required`;
  missing deltas are not invented.
- **Disconnects are not cancels.** Turn commands run under a
  server-scoped cancellable context, not the socket's — closing the
  socket never aborts a running or queued turn. Recovery is
  `turn.watch` + HTTP re-read.

## Frame model

All messages are **JSON text frames**; binary frames are not used.
Ping/pong uses the protocol's own control frames, not JSON.

Common envelope fields:

| Field | Present on | Meaning |
|---|---|---|
| `type` | every frame | Frame kind, e.g. `turn.send`. |
| `requestId` | every client command; echoed on that command's replies | Client-chosen opaque correlation id (recommend unique per command). |
| `conversationId` | commands and all turn frames | The room the turn belongs to. On `turn.send` it may be `null`/absent — the server mints one. |
| `turnId` | all turn frames once allocated | Server-minted `t_<base32>` id, unique per turn. |
| `seq` | turn lifecycle and stream frames | Per-turn strictly increasing integer sequence — see [sequencing](#sequencing-and-ordering). |
| `payload` | commands and most events | Frame-specific body. |

## Client → server

### `turn.send` — submit a user message

```json
{
  "type": "turn.send",
  "requestId": "req-01",
  "conversationId": null,
  "payload": {
    "providerId": "openrouter",
    "modelId": "openai/gpt-5",
    "content": "the cache test still flakes — look at invalidate()",
    "effort": "high",
    "upstream": "DeepInfra",
    "environment": "local",
    "workspace": "/home/alice/project",
    "references": [{ "kind": "file", "id": "/home/alice/project/cache.go", "label": "/cache.go", "start": 0, "end": 9 }],
    "uploads": ["u_7Q2…"],
    "annotations": [{ "pickId": "pk_3F…", "comment": "this overflows", "screenshot": true }],
    "recordingId": "rec_9H…"
  }
}
```

- `conversationId` — `null`, absent, or a client-minted **path-safe** id.
  When absent the server mints a `c_<base32>` domain id *immediately*,
  before the job is scheduled — `turn.opened` always names the room.
- `payload.providerId`, `payload.modelId` — optional overrides. On a brand-new
  conversation both are required (nothing is bound yet — a missing
  binding surfaces as `turn.failed` `400` when the job runs); on an
  existing one `""`/absent reuses the stored binding. A successful turn
  re-binds the overrides to the room.
- `payload.effort` — optional reasoning-effort choice for the turn:
  `default`, `none`, `low`, `medium`, `high`, `xhigh`, `max`. `default`
  asks for the model's configured effort. Absent uses the room's stored
  effort (the picker's, kept on the room), so a turn started from a chat
  or an automation runs the way the owner set the room up. A successful
  turn stores what it named as the room's effort.
- `payload.upstream` — optional upstream-backend pin for aggregator
  routes (e.g. OpenRouter's `provider.order`). Absent or `""` uses the
  room's stored pin, which is empty (the provider decides) until one was
  chosen; clearing it is a `PATCH /api/conversations/{id}` with
  `"upstream": ""`. A successful turn stores a pin it named.
- `payload.workspace` — optional absolute directory the room's tools work
  in. The selected environment's workspace repository validates it when the
  turn runs; an invalid path ends in `turn.failed` `400`. A successful turn
  re-binds it to the room — per-conversation, like provider/model.
- `payload.environment` — optional `local`, `ssh`, or `docker`; absent keeps
  the room's binding (`local` for a new room). An unknown word is rejected
  at command validation. A known but unregistered driver ends in
  `turn.failed` `400` before a user message is persisted. Changing the
  environment without a workspace override clears the previous driver's
  workspace path so the new driver's default applies. Only `local` is
  registered by the current binary.
- `payload.agentId` — optional id of a configured external agent. It moves
  the room onto that agent: the agent runs the turn with its own tools, and
  `payload.modelId` names the agent's model (empty keeps the agent's
  choice). `providerId`, `effort`, and `upstream` do not apply. Naming a
  `providerId` without `agentId` moves an agent room back onto that provider. A room may
  switch either way at any turn; an agent that did not see the turns in
  between starts a fresh session with a recap of the conversation.
- `payload.projectId` — optional, new rooms only: opens the room inside that
  project (`400` for an unknown id). A room in a project works in the
  project's primary folder; agents also get its other folders. Existing
  rooms move between projects with `PATCH /api/conversations/{id}`.
- `payload.content` — the text. It may be empty only when the message
  carries uploads, annotations, or a recording. No `file://` or `data:`
  URIs cross the socket: files arrive through `POST /api/uploads` first.
- `payload.references` — optional selections the owner made in `content`
  (`@` and `/` in the composer), each `{kind, id, label, start, end}` with
  byte offsets: `label` must be exactly `content[start:end]`, references
  ascend without overlap, and `kind` is `file`, `folder`, `skill`, `mcp`,
  `command`, `conversation`, or `project` (`invalid_argument`, or
  `turn.failed` `400` when they do not fit the text). A model reads a
  "Selected references:" note after the text; an agent gets the same note
  and a resource link for each file. A non-command selection that starts
  the text with `/` is prefixed `Reference: ` for an agent, so it does not
  read as a command.
- `payload.uploads` — optional ids from `POST /api/uploads`, at most 10;
  each becomes an attachment pinned by its sha256. An unknown id is
  `invalid_argument`.
- `payload.annotations` — optional marks made in the thread's live view
  (`POST /api/conversations/{id}/browser/annotate`), at most 20, each
  `{pickId, comment?, screenshot?}`. A pick from another thread, or an
  unknown one, is `invalid_argument`. The model and the agent read an
  `<annotation-context>` note with one `[N]` line per mark; each kept
  screenshot (the page with badge N painted on it) rides after the
  uploads, in order.
- `payload.recordingId` — optional id of one of the thread's browser
  recordings (`GET /api/conversations/{id}/recordings`). A recording from
  another thread, or an unknown id, is `invalid_argument`. The message
  stores the id, status, and a summary read at send time — a live
  recording's is a brief of its counts, since its summary is written at
  stop — and the model reads a `<recording-context>` note after the
  text. `turn.steer` rejects a `recordingId`: a recording belongs to a
  new message.
- A command that fails **syntactic validation** — unparseable JSON,
  unknown `type`, missing or ill-typed required fields, a non-path-safe
  `conversationId`, empty `content`, or an unknown `effort` or `environment`
  word — is rejected
  with an `error` frame
  (`code: "invalid_argument"`). No `turnId` is allocated, nothing is
  scheduled, and no `turn.opened`/`turn.failed` exist for it — it never
  became a turn.
- A syntactically valid command is **accepted**: the server allocates a
  `turnId`, emits `turn.opened`, and schedules an asynchronous
  submission. From that point the turn always ends with exactly one
  terminal `turn.completed` or `turn.failed` — including submission-time
  failures such as a full room queue (`429`) or a deleted room (`410`).

### `turn.edit` — rewrite a user message and regenerate

```json
{
  "type": "turn.edit",
  "requestId": "req-02",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "payload": {
    "messageId": "m_9p2xv7kq4f1nz8w0t6jb5r3yce",
    "content": "actually, look at invalidate() in cache.go",
    "providerId": "openrouter",
    "modelId": "anthropic/claude-sonnet-4.5",
    "effort": "high"
  }
}
```

- Same lifecycle as `turn.send` (`turn.opened` → stream → terminal).
- `payload.providerId`, `payload.modelId`, `payload.effort`,
  `payload.upstream`, `payload.workspace` — optional per-turn overrides
  with the same contract as `turn.send` (`""`/absent keeps the stored
  binding; an unknown `effort` word is `invalid_argument`; a successful
  turn re-binds `providerId`/`modelId`/`workspace`/`effort`/`upstream` to
  the room). `environment` is not accepted — an edit never re-homes the
  room.
- A room bound to an external agent takes the edit too, with a payload of
  `messageId` and `content` (plus `workspace`): the payload names no agent,
  because the edit runs on the room's own, and a field the payload does not
  list — `agentId` included — is `invalid_argument`. An agent has no edit of
  its own, so everything recorded after the target is dropped, the agent's
  session is forgotten, and the agent answers the edited line in a fresh
  session that is given a recap of the earlier thread; the old session is
  removed from the agent once the turn has run (never when the agent
  handed back the same id). What the agent changed on disk in the dropped
  turns stays changed, and the agent is told so.
- Only user messages are editable — a non-user `messageId` ends the turn
  `turn.failed` `400`; an id that exists nowhere in history is `404`.
- **Preempts:** the edit interrupts the room's running job and cuts ahead
  of queued sends, which then run against the rewritten history. The
  preempted turn ends `turn.failed` `408`.
- Terminal frames for an edit never claim `userMessagePersisted` (the
  edit rewrites an existing line, it does not append one), and
  `resyncRequired` is always `true` — the rewrite drops everything
  recorded after the target.

### `turn.resume` — continue from current history without rewriting

```json
{
  "type": "turn.resume",
  "requestId": "req-02b",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "payload": {
    "providerId": "openrouter",
    "modelId": "anthropic/claude-sonnet-4.5",
    "effort": "high"
  }
}
```

- Same lifecycle as `turn.send` (`turn.opened` → stream → terminal).
- Continues the room's current history: unanswered origin tool calls
  finish first, then the model is called again. Nothing is rewritten or
  truncated — prior tool results and partial replies stay. In a room bound
  to an external agent the payload is empty (or `workspace`): the agent
  already holds its own record of the dialogue, so only a user message that
  never got a reply is handed to it again, and anything else is `409`
  ("nothing to retry").
- `payload.providerId`, `payload.modelId`, `payload.effort`,
  `payload.upstream`, `payload.workspace` — optional per-turn overrides
  with the same contract as `turn.edit`. Carrying the current selection
  is what lets a retry escape the model that just failed. `environment`
  is not accepted — a resume never re-homes the room. An empty room is
  `turn.failed` `400`; a missing room is `404`.
- **Preempts:** like `turn.edit`, resume interrupts the room's running
  job and cuts ahead of queued sends. The preempted turn ends
  `turn.failed` `408`.
- Terminal frames never claim `userMessagePersisted` (resume appends no
  user line), and `resyncRequired` is always `true`.

### `turn.cancel` — cancel one turn

```json
{
  "type": "turn.cancel",
  "requestId": "req-03",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf"
}
```

- Targets **one exact turn** — identified by `turnId` — including while
  it is still sitting in the room's queue. The hub cancels the
  server-scoped context it created for that turn: the existing job
  contract already aborts a queued submission before it touches anything
  and interrupts a running one, so the room-wide
  `ConversationApp.Cancel` — which interrupts *whatever happens to be
  running* on the room — is never invoked here.
- Reply: `turn.cancel_ack` — meaning **the request was accepted**, nothing
  more. The canceled turn still emits its own terminal — normally
  `turn.failed` `408`, but if the job had already committed before the
  context was observed the terminal may be `turn.completed`. An ack is
  never a guarantee of interruption.
- An unknown, finished, or foreign `turnId` answers an `error` frame
  (`code: "not_found"`), not an ack.

### `turn.steer` — submit steering input into an exact turn

```json
{
  "type": "turn.steer",
  "requestId": "req-06",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "payload": { "content": "also check the migration path" }
}
```

- Submits `content` into the **mailbox of the exact `turnId` named** —
  never implicitly "the current turn". The turn must be a live
  `turn.send` turn owned by this hub: unknown, finished, or
  foreign-conversation ids answer `error`/`not_found`; edit turns
  answer `error`/`invalid_argument` (`"that turn does not take
  steering"`).
- The payload uses the same user input as `turn.send`: `content`,
  selected `references` (UTF-8 byte offsets), `uploads`, and
  `annotations`. A message can contain only attachments or
  annotations. `recordingId` is refused (`invalid_argument` — a
  recording belongs to a new message; stop the turn and send it).
- The command's `requestId` **is the input's `inputId`** — every ack,
  recall, fold frame, and snapshot names it. `requestId` is bounded to
  128 bytes on steering controls (a hard `invalid_argument` past it).
- A journaled `turn.steer_ack` records acceptance for watchers —
  **accepted is never consumed**. The issuing socket also gets an
  answer: the sequenced frame when it is subscribed, an unsequenced
  command reply when it is not, so acceptance is never silent. The
  input is consumed only when the turn folds it at an inference safe
  point and persists it as an ordinary user message (surfacing as a
  `turn.steer` stream chunk). In an agent room the agent's own loop has
  no such point: an agent that takes input mid-turn gets it at once and
  the input settles `persisted` on delivery (it is written with the
  reply, as a `steer` part where the agent took it); any other agent is
  first asked to yield at its next safe point — once every reported
  tool call has finished and no approval is pending — and the job waits
  for that original prompt to end (naturally, when the agent cannot
  yield) before sending the inputs still pending as one follow-up
  prompt in the same job.
- **Idempotent dedup:** the same `requestId` + the same complete input
  against the same turn re-acks without a second submission — as an
  unsequenced reply, not a new journaled frame; the same `requestId`
  with *different* input is `invalid_argument`. The same input under
  a *new* `requestId` is a distinct input — identical submissions are
  never ambiguous because correlation is by `inputId`.
- **Bounded:** at most 32 steering submissions per turn lifetime — past
  the cap the command is rejected before acceptance with
  `error`/`queue_full`; nothing is silently truncated.
- An unconsumed input (`pending` or `claimed` when the turn ends) is
  never delivered to a later turn as steering. The WebUI returns its
  complete input to the draft rather than adding it to the send queue.

### `turn.steer_recall` — withdraw a pending steering input

```json
{
  "type": "turn.steer_recall",
  "requestId": "req-07",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "payload": { "inputId": "req-06" }
}
```

- Withdraws the input `inputId` **only while it is still pending** —
  not yet claimed at a safe point. Success emits the journaled
  `turn.steer_recalled`; the client owns the full input again.
- A claimed or persisted input answers `error`/`not_found`
  (`"steering input already consumed"`) — consumed input is never
  restored by recall.
- Re-recalling an already-recalled input answers with an unsequenced
  `turn.steer_recalled` reply — recall is idempotent, so a lost ack
  reconciles safely on retry without growing the journal.
- `payload.inputId` must be the `requestId` of a `turn.steer`
  previously accepted on this turn; anything else is `not_found`.

### `turn.watch` — attach / reattach to a turn

```json
{
  "type": "turn.watch",
  "requestId": "req-04",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "afterSeq": 12
}
```

- Attaches this socket to the turn and replays journaled frames with
  `seq > afterSeq` verbatim (original `requestId`s included) when the
  window fits the replay budget (half the socket's outbound queue).
  `afterSeq: -1` asks from `seq` 0, but a journal deeper than that budget
  is answered with one `turn.resync_required` carrying the bounded live
  snapshot (or the terminal verdict) instead of a frame dump; an active
  turn still attaches for live frames afterwards. This is what keeps a
  tab opening a long agent turn from flooding its own socket.
- If the turn is unknown or evicted, the `turnId` belongs to a different
  conversation, `afterSeq` exceeds the latest `seq`, or a retained range
  has a gap at `afterSeq`, the reply is `turn.resync_required` — the
  client must then re-read via `GET /api/conversations/{id}` and
  `GET .../messages` and render **persisted** state. Missing deltas are
  never fabricated.
- On a **gap in an active turn** the socket answers
  `resync_required` and still attaches for live frames — the tab sees
  the terminal while it re-reads history; a finished turn with a gap
  answers `resync_required` and attaches nothing.
- Watching a finished turn replays a journal within the budget (possibly
  through its terminal frame) or answers `resync_required` with the
  terminal verdict.

## Server → client

### Command replies

`turn.opened` — transport acceptance of a send/edit. It means exactly
this: the command was syntactically valid, ids were allocated, and an
asynchronous submission was scheduled. It says **nothing** about the
room worker — the underlying application `Send`/`Edit` call blocks
until the job completes, so the hub cannot truthfully claim the job was
even queued when `opened` goes out. A full mailbox (`429`), a deleted
room (`410`), an unbound provider (`400`), or a non-editable `messageId`
all surface later as the terminal `turn.failed`. `seq: 0` is always the
first frame of a turn, and the server must write `opened` to the socket
and the journal **before** any later frame of that turn can be emitted —
each socket's writes go through one serialized writer with a bounded
buffer. The payload carries `startedAt`, the server's admission
wall-clock in unix milliseconds — a watcher that replays or reattaches
adopts it, so a working timer shows the turn's real age, not the tab's
attach time.

```json
{
  "type": "turn.opened",
  "requestId": "req-01",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 0,
  "payload": { "startedAt": 1763851200000 }
}
```

`turn.cancel_ack` — a `turn.cancel` was accepted (no `seq`; it is a
reply to the cancel command, not part of the turn stream):

```json
{
  "type": "turn.cancel_ack",
  "requestId": "req-03",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf"
}
```

`turn.steer_ack` — a `turn.steer` submission was accepted into the
turn's mailbox. It is a **journaled turn frame** (it carries `seq` and
replays on `turn.watch`), so a lost live ack recovers on reattach —
unlike `turn.cancel_ack` it is part of the turn's ordered record.
Issuing sockets that are not subscribed also receive an **unsequenced**
reply echoing their `requestId`; idempotent retries re-ack the same
way. Accepted is never consumed: the receipt resolves at the fold or
the terminal snapshot.

```json
{
  "type": "turn.steer_ack",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 4,
  "payload": { "inputId": "req-06" }
}
```

`turn.steer_recalled` — a `turn.steer_recall` withdrew the input while
it was still pending. Journaled and sequenced like the ack on the
first recall; idempotent re-recalls and unattached issuers answer with
an unsequenced reply, so the client can always converge on "this input
is withdrawn" without the journal growing per retry.

```json
{
  "type": "turn.steer_recalled",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r3yhd",
  "seq": 6,
  "payload": { "inputId": "req-06" }
}
```

`turn.resync_required` — a `turn.watch` could not be satisfied. The
payload reports what the hub still knows about the turn: `turn` is
`"active"` while the turn runs, the terminal verdict (`"completed"` /
`"failed"`) for a retained finished turn — with the stored terminal
body replayed under `terminal` — and `"unknown"` for an evicted or
never-seen id. `steering` is the same bounded receipt snapshot the
terminal frame carries, so a client whose ack or recall result was
lost reconciles submitted inputs against it and against history —
never by resending an unknown outcome.

For an `"active"` turn the payload also carries the live snapshot:
`startedAt` (unix ms, same clock `turn.opened` reports), `phase` (the
last reported phase, when one is running), and `parts` — the turn's
bounded shadow document of what already streamed, folded to ordered
segment records (`text`, `reasoning`, `tool`, `steer`, `plan`,
`media`, `approval`, `elicitation`) in the same shapes the stream
frames project. The client renders `parts` and keeps appending live
frames after it; where the document's bound had to release older
text, `dropped` counts the bytes so recovery is stated honestly rather
than faked complete.

The same receipt state means different things by `turn` verdict. In an
`"active"` snapshot, `pending` and `claimed` inputs are **still owned
by the server** — the turn may yet fold them, so the client keeps the
receipt and must not restore the input yet. Only in a terminal context (a
`completed`/`failed` verdict or a terminal frame's own `steering`
array) do `pending`/`claimed` act as the fallback signal "the turn
ended without committing this input — the client can restore the whole
input to the draft". An input absent from an active snapshot is
*unknown*; its receipt stays open for a later frame to resolve.

```json
{
  "type": "turn.resync_required",
  "requestId": "req-04",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "payload": {
    "turn": "active",
    "startedAt": 1763851200000,
    "steering": [
      { "inputId": "req-06", "state": "pending" },
      { "inputId": "req-08", "state": "persisted" }
    ],
    "parts": [
      { "kind": "text", "text": "Looking at the repo…" },
      { "kind": "tool", "callId": "call_1", "name": "exec",
        "input": "{\"command\":\"ls\"}", "output": "docs\nsrc", "status": "streaming" }
    ],
    "dropped": 0
  }
}
```

`error` — a command rejection that belongs to no turn: unparseable JSON,
an unknown `type`, a syntactically invalid command (missing or ill-typed
required fields, a non-path-safe `conversationId`, empty `content`), or
`turn.cancel` naming an unknown or finished `turnId`. `requestId` is
echoed when it could be parsed. There is no `turnId` and no `seq` —
nothing was allocated — and no turn frames follow.

```json
{
  "type": "error",
  "requestId": "req-05",
  "code": "not_found",
  "message": "no live turn with that id"
}
```

### Room changed

`conversation.updated` — a room-level notice **broadcast to every
connected socket** when a room's stored history changed outside a turn:
a reply was delivered to a linked chat with files, or the chat sent a
delivered/read receipt for it. Like `conversation.activity` it carries no
`seq`, is never journaled, and has no payload; a client showing the room
reloads its newest messages to pick up `sentFiles` and `receipt`.

```json
{ "type": "conversation.updated", "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd" }
```

### Room activity

`conversation.activity` — a room-level notice **broadcast to every
connected socket** when a turn opens or reaches its terminal. It is not
part of the turn's ordered record: it carries no `seq`, is never
journaled, and a `turn.watch` replay never reproduces it. The `opened`
notice is enqueued under the same lock as `turn.opened`, so on each
socket it provably follows that turn's `seq: 0`; the terminal notice
likewise follows the terminal frame. Broadcasts are emitted only on
these two edges — command replies (`turn.cancel_ack`, watch attach,
resync) change no room state and send nothing.

```json
{
  "type": "conversation.activity",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "payload": { "state": "opened", "kind": "send", "requestId": "req-01" }
}
```

- `state` — `"opened"` when the room gained a live turn; `"completed"`
  or `"failed"` mirroring the turn's terminal type when it ended.
- `kind` — `"send"` or `"edit"`, the command that opened the turn.
- `payload.requestId` — the **opening command's** requestId. The
  issuing tab receives the same notice and recognizes its own by this
  field — its send/edit flow already refreshes, so only foreign
  notices drive discovery work. Opaque to every other tab. The frame
  itself carries no top-level `requestId` — it answers no command.

### Stream frames

One turn's frames, in order. Every stream frame carries `conversationId`,
`turnId`, and `seq`.

`turn.delta` — incremental assistant text:

```json
{
  "type": "turn.delta",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 1,
  "payload": { "text": "Found it — " }
}
```

`turn.phase` — a coarse status marker. `phase` is `"reasoning"` while the
model produces reasoning: one marker per reasoning stretch, no matter how
many chunks the provider streams. It is `"retry_wait"` while the harness
pauses before retrying a transient provider failure (rate limit or upstream
5xx), and `"compacting"` while the harness folds history to fit the context
window before a model call. A `retry_wait` marker carries `retryAfterMs`,
the pause length; the other phases carry none. Any stream frame that follows
ends the wait.

```json
{
  "type": "turn.phase",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 4,
  "payload": { "phase": "retry_wait", "retryAfterMs": 45000 }
}
```
All provider reasoning text, including raw `reasoning_content` and
`reasoning_text` as well as provider-supplied summaries, crosses as
`turn.reasoning` chunks in the same order as other stream events:

```json
{
  "type": "turn.phase",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 2,
  "payload": { "phase": "reasoning" }
}
```

`turn.reasoning` — one bounded chunk of provider reasoning text. Every
nonempty provider chunk crosses **in order**; chunks
larger than the 32 KiB outbound bound are split UTF-8-safely, so
reassemble by concatenating `text` fields in `seq` order. The text is
untrusted display content — render it escaped, never as markup:

```json
{
  "type": "turn.reasoning",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 3,
  "payload": { "text": "I'll compare the two cache paths…" }
}
```

`turn.tool_call` — the model requested a tool. This frame carries only
call metadata, so oversized arguments can never turn the request itself
into an oversized frame:

```json
{
  "type": "turn.tool_call",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 3,
  "payload": { "callId": "call_9f2a", "name": "read_file" }
}
```

`turn.tool_input` — one bounded chunk of the exact `input` argument
string the model produced. Chunks follow the owning `turn.tool_call`
in order; concatenate `text` in `seq` order to reconstruct the argument
before rendering or copying it:

```json
{
  "type": "turn.tool_input",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 4,
  "payload": { "callId": "call_9f2a", "text": "{\"path\":\"README.md\"}" }
}
```

`turn.tool_output` — a live output chunk for the identified call. Every
nonempty provider chunk is delivered **in order**; chunks larger than
the 32 KiB outbound bound are split UTF-8-safely into multiple frames,
so a burst or a large output produces one frame per bounded chunk —
reassemble by concatenating `text` fields in `seq` order. This is
progress content, **not** proof the call finished: the terminal outcome
is its own `turn.tool_result` frame.

Tool input and output are arbitrary data — they can legitimately
contain credentials, local paths, or commands. They cross because this
is a single-user loopback UI for its owner; treat them as untrusted
display text, never markup. The same data persists in the call's record
and is retrievable over HTTP history (`input`/`output`/`calledAt`).
Frames carry JSON strings, not binary: malformed UTF-8 emitted by a tool
is normalized by JSON encoding and is not promised byte-exact.

```json
{
  "type": "turn.tool_output",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 4,
  "payload": { "callId": "call_9f2a", "text": "partial output chunk" }
}
```

`turn.tool_result` — the identified call reached a terminal outcome.
`failed` is the executor's own verdict, not a guess parsed from the
output text; an empty successful result still gets this frame so a live
row can leave the running state. A call that produced media (`read_media`,
a browser screenshot) lists it in `attachments`, as the history API does
(`mediaType`, `modality`, `uri`, `name`): references only, which the client
fetches through the files API. It precedes `turn.step_finished`.

```json
{
  "type": "turn.tool_result",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 5,
  "payload": { "callId": "call_9f2a", "failed": false }
}
```

A call that produced an image:

```json
{
  "type": "turn.tool_result",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 6,
  "payload": {
    "callId": "call_a1b2",
    "failed": false,
    "attachments": [
      { "mediaType": "image/png", "modality": "image", "uri": "file:///tmp/frame.png", "name": "frame.png" }
    ]
  }
}
```

`turn.steer` — a user steering line folded into the running turn at an
inference safe point (after a tool result persisted, before the next
provider call). The text lands in history as an ordinary user message
**before** this frame emits — the fold persists first — so the frame
deliberately carries no message id: a `resyncRequired` history re-read
supplies persisted ids, and correlating by `inputId` is enough to tell
the client's own inputs apart. Reassemble
by concatenating `text` fields in `seq` order like any chunked
content — an attachment-only input has empty text and still emits one
frame. `attachments` on the **first chunk** lists the uploads the
input carried, in the same shape `turn.tool_result` uses; later chunks
omit it. `inputId` correlates the fold to the `turn.steer` command
that submitted it — absent for receipt-free input (the CLI's
`/steer`). `final` marks the last chunk of one input, so identical
consecutive inputs render as separate segments, never concatenated.

```json
{
  "type": "turn.steer",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 5,
  "payload": { "text": "also check the migration path", "inputId": "req-06", "final": true }
}
```

`turn.step_finished` — one provider call ended. This maps the domain's
`TurnEventFinished`, which is **per provider call, not per turn** — a
tool-using turn emits several of these. `finishReason` is one of `stop`,
`tool_calls`, `length`, `content_filter`; `usage` is the same shape as
the HTTP `Usage` projection:

```json
{
  "type": "turn.step_finished",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 5,
  "payload": {
    "finishReason": "tool_calls",
    "usage": {
      "inputTokens": 4120,
      "cacheReadTokens": 0,
      "cacheWriteTokens": 0,
      "outputTokens": 203,
      "totalInputTokens": 4120,
      "totalTokens": 4323
    }
  }
}
```

`turn.approval` — the running agent asks the owner for permission to run
one of its tool calls. The owner answers over HTTP with
`POST /api/conversations/{id}/approvals/{requestId}` `{"optionId": …}`;
`GET /api/conversations/{id}/approvals` lists the questions still open.
`input` is the agent's own description of the call, display text only,
bounded to 32 KiB. `choice` is `allow_once`, `allow_always`, `reject_once`,
or `reject_always`.

```json
{
  "type": "turn.approval",
  "conversationId": "c_…",
  "turnId": "t_…",
  "seq": 6,
  "payload": {
    "id": "apr_…",
    "callId": "toolu_…",
    "title": "Read workspace files",
    "input": "{\"path\":\".\"}",
    "action": "read",
    "options": [
      { "id": "allow-once", "name": "Allow once", "choice": "allow_once" },
      { "id": "reject-once", "name": "Reject", "choice": "reject_once" }
    ]
  }
}
```

`turn.approval_resolved` — a question closed. `optionId` is the chosen
answer, or `""` when the turn ended or was cancelled before an answer.
Requests the agent's approval policy or a room's approval rule answers
itself never reach the owner and produce neither frame.

`turn.elicitation` — the running agent asks the owner for input: a form
(`mode: "form"`, `schema` a JSON schema object of flat fields) or a page to
visit (`mode: "url"`). The owner answers over HTTP with
`POST /api/conversations/{id}/elicitations/{elicitationId}`
`{"action": "accept" | "decline" | "cancel", "content": {…}}`.
`turn.elicitation_resolved` closes it with the `action`, or `""` when the
turn ended first. What the owner entered is never stored; the room's
history keeps the request and its outcome.

```json
{
  "type": "turn.elicitation",
  "conversationId": "c_…",
  "turnId": "t_…",
  "seq": 4,
  "payload": {
    "id": "eli_…",
    "mode": "form",
    "message": "Which environment should I deploy to?",
    "schema": { "type": "object", "properties": { "environment": { "type": "string", "enum": ["staging", "production"] } }, "required": ["environment"] }
  }
}
```

`turn.plan` — the agent's plan, whole each time: `entries` is
`[{content, status, priority}]` with `status` `pending`, `in_progress`, or
`completed`. A later frame replaces the earlier plan.

`turn.notice` — something an agent or the runtime wants the owner to
know, which is not part of the reply: `severity` is `info`, `warning`, or
`error`, `title` is optional, and `text` is the message. A frame comes
from an agent's own notices, from its context compaction ("Compacting
context"), from a session the agent could not restore ("Session
recovered"), and from an agent that has not yet stopped when the owner's
message asked it to. Clients show it as a toast; nothing is stored.

```json
{
  "type": "turn.notice",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 4,
  "payload": { "severity": "warning", "title": "Heads up", "text": "The agent is still working." }
}
```

`turn.media` — a picture, sound, or file an agent put in its reply, at the
point it arrived. The payload is an attachment reference, as history
serves it (`mediaType`, `modality`, `uri`, `name`); the client opens it
through the files API. At most eight arrive per turn. The same file is
kept with the reply as a `media` message part, and goes to a linked chat
with the reply's text.

```json
{
  "type": "turn.media",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 6,
  "payload": { "mediaType": "image/png", "modality": "image", "uri": "file:///data/uploads/u_1/chart.png", "name": "chart.png" }
}
```

`turn.context` — an agent's own account of its session's context window,
from its ACP `usage_update`: `used` tokens are held now, `size` is the
window, and `cost` (`{amount, currency}`) is the session's cost so far when
the agent prices its work. Each frame replaces the last. It feeds the
composer footer and is not part of the transcript; an agent that sends no
`usage_update` sends no frame.

```json
{
  "type": "turn.context",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 9,
  "payload": { "used": 53000, "size": 200000, "cost": { "amount": 0.0421, "currency": "USD" } }
}
```

### Terminal frames

Exactly one per turn, always the last frame.

`turn.completed` — emitted **only** after the application call returned
success and all persistence for the turn finalized. A `turn.step_finished`
is never a substitute: the domain finished event ends one provider call;
only the job's return ends the turn.

```json
{
  "type": "turn.completed",
  "requestId": "req-01",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 9,
  "payload": {
    "userMessagePersisted": true,
    "steering": [
      { "inputId": "req-06", "state": "persisted" },
      { "inputId": "req-08", "state": "pending" }
    ],
    "resyncRequired": true
  }
}
```

- `userMessagePersisted` — present on send turns, always `true` at
  `turn.completed` (a send cannot complete without committing the user
  line). Absent on edit turns.
- `steering` — on send turns, the bounded terminal receipt snapshot:
  every input submitted to this turn's mailbox with its final state —
  `persisted` (folded and committed), `claimed` (drained but never
  committed), `pending` (never drained), or `recalled`. Content never
  re-crosses — the client owns the text it submitted; the snapshot only
  says what happened to it. An unconsumed input is never silently
  delivered to a later turn.
- `resyncRequired` — **always `true`** on an accepted turn's terminal
  frame. Live frames never carry persisted message ids, tool results, or
  rewritten history, so a client MUST re-read `GET
  /api/conversations/{id}` and `GET .../messages` after every terminal
  frame rather than reconstructing state from the stream.

`turn.failed` — the turn ended unsuccessfully. This is terminal for
**every** failure mode of an accepted turn: queue rejection,
cancellation, provider errors, engine faults — a client never waits
indefinitely after `turn.opened`. (A command rejected before
allocation gets `error`, not this frame — there is no turn to fail.)

```json
{
  "type": "turn.failed",
  "requestId": "req-01",
  "conversationId": "c_k4j9xm2p8q1vz7w0t6nb5r3yhd",
  "turnId": "t_3kd9xm2p8q1vz7w0t6nb5r4yhf",
  "seq": 9,
  "payload": {
    "status": 502,
    "code": "upstream_error",
    "message": "provider rejected the request (HTTP 502)",
    "userMessagePersisted": true,
    "resyncRequired": true
  }
}
```

- `status` — the same HTTP-shaped status the REST surface would return
  for the failure: `400` invalid, `404` missing, `408` canceled,
  `410` deleted-underneath, `413` provider context overflow, `429` room
  queue full (or upstream rate-limit, `code` `upstream_error`), `500`
  internal, `502`/`504` upstream, `503` shutting down.
- `code` — the same stable code strings as the HTTP problem catalog
  (`invalid_argument`, `not_found`, `interrupted`, `gone`,
  `input_too_large`, `queue_full`, `internal`, `upstream_error`,
  `unavailable`, `upstream_timeout`). Command-level `error` frames may
  additionally carry `unimplemented` for a recognized command whose
  phase has not landed yet (none today — reserved for future commands)
  and `queue_full` for turn admission, not just room mailboxes.
- `message` — display-safe only; **internal causes are never exposed**.
- `userMessagePersisted` — on send turns, whether the user line
  committed before the failure (`true` = it is in history even though
  the turn died — the failed-send receipt). On edit turns it is absent
  or `false`, never claimed.
- `steering` — on send turns, the same terminal receipt snapshot as
  `turn.completed`: a `claimed` or `pending` state here means the input
  never committed — the text is the client's to restore or requeue.
- `resyncRequired` — always `true`, same as `turn.completed`: re-read
  persisted state over HTTP after every terminal frame.

## Sequencing and ordering

- `seq` is **per turn**, starting at `0` (`turn.opened`) and strictly
  increasing by one for every subsequent frame of that turn, terminal
  frame last. `turn.cancel_ack`, `turn.resync_required`, and `error`
  are command replies and carry no `seq`; `conversation.activity` is a
  room-level broadcast and likewise carries none.
- Frames of one turn are totally ordered. **There is no cross-turn
  ordering guarantee** — interleaved frames of two turns are valid, and
  clients correlate by `turnId`, never by arrival position.
- `requestId` correlates a command to its `turn.opened`, terminal frame,
  `turn.cancel_ack`, or `turn.resync_required`/`error` reply — whichever
  applies. Stream frames mid-turn do not repeat it (they attribute via
  `turnId`).

## Liveness, size, and backpressure

- **Admission is bounded.** The hub admits a fixed number of concurrent
  turns; beyond it a `turn.send` is rejected before any work or id
  allocation with a pre-allocated `error`/`queue_full` reply. The
  admission cap is a deployment constant — the contract guarantees only
  that the bound exists.
- **Ping/pong** ride the WebSocket control frames: the server pings on a
  regular interval and drops a socket that misses its pong deadline;
  client pings are answered. Intervals are implementation constants,
  not negotiated.
- **Frame size** is bounded in both directions. An oversized inbound
  frame is a connection-level violation: it is rejected with an `error`
  frame when parseable, and the socket may be closed with a
  policy-violation or too-big close code — it is *not* a `turn.failed`
  `413` (that status in a terminal frame means an application/provider
  payload limit, not frame size). Byte limits are deployment constants;
  the contract guarantees only that they exist.
- **Backpressure:** the domain's `TurnEventHandler` is synchronous — a
  blocked consumer stalls the provider read loop. The hub therefore
  buffers per subscriber with a bounded queue; on overflow it terminates
  *that subscriber's delivery* (close the socket, or drop it from the
  turn's fan-out), never the turn and never other subscribers.
- **Stream frames are bounded too.** A single provider delta, tool
  argument, or tool output burst is split into multiple frames rather
  than allowed to dwarf the journal or one subscriber's queue — chunks
  are cut on UTF-8 boundaries so reassembly is byte-exact.
- **The journal is bounded.** Each turn keeps only a bounded tail of
  frames (frame count and byte caps, plus an age limit on finished
  turns) — enough for short-window `turn.watch` reattachment, never
  unbounded growth. A window the journal can't satisfy honestly is a
  `resync_required`, never fabricated deltas.

As implemented today the constants are: max 32 concurrent turns, max 64
concurrent sockets (a full hub rejects the upgrade with `503`
problem+json before the handshake), 64 KiB inbound frames, 64 queued
outbound frames per socket, a hard 1 MiB cap on any single outbound
frame (a frame that can't fit — e.g. oversized provider-originated
metadata — terminates that turn with a sanitized `turn.failed` `413`),
a journal of 512 frames / 4 MiB / 2 minutes post-terminal retention,
pings every 25 s with a 60 s pong deadline, a 10 s write deadline,
32 steering submissions per turn lifetime, requestIds of at most 128
bytes on `turn.steer` and `turn.steer_recall`, and
`turn.delta`, `turn.reasoning`, `turn.tool_input`, `turn.tool_output`,
and `turn.steer` text chunks of at most 32 KiB.

## Lifecycle races, catalogued

- **`opened` before any real work.** `turn.opened` only means the
  command was accepted and scheduled — the job may still bounce off a
  full mailbox (`429`), a deleted room (`410`), an unbound provider
  (`400`), or a non-editable `messageId` (`400`/`404`); every such
  outcome arrives as the terminal `turn.failed`.
- **Disconnect mid-turn.** Nothing aborts; the turn runs to its terminal
  state against the server-scoped context. Reconnect + `turn.watch`
  (or plain HTTP re-read) recovers it.
- **Cancel racing completion.** `turn.cancel` against a turn that already
  finished answers `error`/`not_found` — there is no retroactive cancel.
- **Edit preemption.** An edit preempts the running turn (`turn.failed`
  `408` on the preempted turn) and queues ahead of pending sends.
- **Delete under queued work.** `DELETE /api/conversations/{id}` retires
  the room; queued turns end `turn.failed` `410 gone`.
- **Failed send with a committed user line.** `turn.failed` may carry
  `userMessagePersisted: true` — the message is in history; render from
  `GET .../messages`, not from the frame stream.
- **Steering ack lost in a disconnect.** `turn.steer_ack` and
  `turn.steer_recalled` are journaled — a `turn.watch` replay re-emits
  them. A submission whose outcome stays unknowable (socket dropped
  before the ack, journal expired) resolves through the steering
  snapshot on `turn.resync_required`/terminal frames plus the HTTP
  history re-read. An input absent from every snapshot and history is
  *unknown* — the client surfaces it and lets the user decide; it never
  auto-resends.
- **Recall racing the safe point.** A `turn.steer_recall` landing after
  the input was claimed answers `not_found` — the input is already on
  its way into history and the fold's `turn.steer` chunk arrives like
  any other stream frame.
- **Shutdown.** A graceful shutdown ends live turns `turn.failed` `503`
  before closing sockets — best-effort only: on an abrupt process exit
  (crash, `SIGKILL`) no terminal frame is guaranteed. After a restart
  the client reattaches with `turn.watch` or re-reads persisted state
  over HTTP; a turn orphaned by a crash resolves to `resync_required` or
  whatever history actually committed.

## Out of scope (initial contract)

- Tool approval prompts or policy questions to the browser — tool
  permission is decided by the domain schema, not negotiated per call.
- Settings or provider change events — poll the HTTP endpoints.
- Room-wide presence events — the only broadcast is the
  `conversation.activity` notice; turn streams stay owner + watchers.
- Attachments of any kind on `turn.send`.
- Binary frames, subprotocols, per-message compression — plain JSON text
  frames only.
