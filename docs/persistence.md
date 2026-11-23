# Persistence and storage

Everything NusaShell writes to disk lives under one storage root and is owned
by `infrastructure/repository/` — the layer responsible for storage format,
hydration, atomic writes, and read projections (see
`infrastructure/repository/AGENTS.md`). This document describes what lands
on disk, in which shape, and why. It is aimed at engineers and agents
debugging a corrupted store, inspecting state by hand, or extending the
format — not at end users.

The guiding idea is that history is an append-only line stream and
everything else is a projection of it. Messages and tool-output patches
share one JSONL stream per conversation, so the common write path is a
single `O_APPEND` + `fsync` and never rewrites stored bytes. The two
exceptions — compaction and edit — rewrite segment files atomically. That
split is what makes the store cheap on the hot path and still recoverable
after a crash.

## Storage root

`Config.Storage.Dir` (`infrastructure/config/storage.go`) is the single
root every repository derives its paths from. It defaults to `./data` via
the `default:"./data"` struct tag and is set in the config file as
`storage.dir`. One root means a deployment moves, backs up, or inspects
all state in a single place.

The layout is wired at the composition root, split by lifecycle:
`admin.LoadBoot` opens `secrets.db` (and a `providers/` repository for
the management plane); `compose` (`cmd/nusashell/wire.go`) wires
`providers/`, `agents/`, `conversations/`, `logs/`, and the application
database `nusashell.db`. These are the real directory names, not
conventions:

```text
<storage.dir>/                  (default ./data)
├── providers/                  one <id>.json per API provider — dir 0700
├── agents/                     one <id>.json per ACP agent — dir 0700
├── conversations/              one subdirectory per conversation
│   └── <convID>/               e.g. c_US57K3ZKE7NJ4TKZOOHCHDBOF4
│       ├── meta.json           record + counters
│       ├── 0.jsonl, 1.jsonl …  sealed archive segments (only after Compact)
│       ├── active.jsonl        the live tail
│       └── tool-output/        created on exec; full-output files only when result exceeds budget
├── logs/
│   ├── active.jsonl            current diagnostic log
│   └── 0.jsonl.gz, 1.jsonl.gz … compressed rotated archives
├── nusashell.db                application database (SQLite, WAL) — file 0600
├── whatsapp.db                 WhatsApp device keys and sessions (whatsmeow) — created on first pairing
├── browser/<profile>/          a browser profile (Chrome user data: cookies, storage, history) — created on first use
├── uploads/<id>/               a file the owner attached to a message (or a live-page mark's screenshot): the file and meta.json (name, media type, sha256)
├── picks/<id>.json             a mark made on a thread's live page, until a message carries it: descriptor, badge number, screenshot upload
├── recordings/<id>/            a browser recording: meta.json, events.jsonl, network.jsonl, summary.txt, artifacts/ (downloads) — kept 7 days; live ones are marked interrupted at start
├── secrets.db                  sqlite key-value store, values sealed — file 0600
└── secrets.key                 the AES-256 key that seals secrets.db — file 0600
```

Transcripts stay in per-conversation files: they are append-heavy, read
by range, and easy to inspect or back up one room at a time. Relational
state — accounts and sessions today; channels, projects, and automations
as they arrive — lives in `nusashell.db`.

Uploads, picks, recordings, and thread views are shared roots, not
per-conversation directories, so deleting one thread leaves them until
their retention or their owner removes them. Deleting **every** thread
(`DELETE /api/conversations`) first deletes each room (preempting a
running turn), then purges all four stores — finalizing live recorders
first — so no artifact is left behind; a room that fails to delete aborts
the global purge and leaves the survivors' files intact.

Conversation and message IDs are minted as `<prefix>_<128-bit base32>`
(`valueobject.NewID` in `infrastructure/domain/valueobject/id.go`). Because
an ID becomes a path segment, every repository re-asserts
`valueobject.ValidID` at its own boundary — no `.`, `..`, or path
separators ever reach `filepath.Join`.

The JSON config file itself is *not* part of this tree. `resolveConfigPath`
(`delivery/cli/admin/admin.go`) takes an exported `CONFIG_PATH` first.
The binary at `~/.local/bin/nusashell` then defaults to
`$XDG_CONFIG_HOME/nusashell/config.json` or `~/.config/nusashell/config.json`
even when the file is missing and even when a `.env` in the working
directory names another path. A source build still lets that `.env` set
`CONFIG_PATH`. When `CONFIG_PATH` is still empty, the same user file is
used if it already exists. When no path is resolved, nothing is created
and `storage.dir` stays `./data`.
`admin.EnsureConfig` generates a file once — called by both `run` and
`serve`. For the user config path it writes `storage.dir` as an absolute
directory (`$XDG_DATA_HOME/nusashell` or `~/.local/share/nusashell`). Any
other path delegates to `setup.EnsureConfig`
(`application/setup/setup.go`), which writes a default-tagged file only
when the path is non-empty and nothing exists there. Read-only admin
commands never create a config. `storage.dir` inside the file selects
the root above. The same `.env` also feeds the `env:` credential
references described below.

When a non-empty config path is configured, loading fails on a missing,
unreadable, or malformed file. Defaults apply to fields absent from a valid
document, not to a failed document read.

## Conversations

A conversation is a directory under `conversations/` named by its ID. Two
file kinds live inside: `meta.json` for record-level state, and a JSONL
line stream split across `active.jsonl` (the live tail) plus any sealed
segments `N.jsonl` written by compaction. The logical history is the
sealed segments in numeric order followed by the live tail.

### `meta.json`

`metaRecord` (`infrastructure/repository/conversation/record.go`) is a
single JSON object holding both the conversation record and the derived
counters `Info` serves, so inspection never has to scan message files:

| Field | Meaning |
|---|---|
| `id` | conversation ID — also the directory name |
| `title`, `providerId`, `modelId` | display title, bound provider ID, bound model ID |
| `effort`, `upstream` | the reasoning effort and the aggregator backend the owner picked for the room, applied to every turn that does not name its own; absent = the model's configured effort / the provider decides |
| `environment`, `workspace` | execution-driver identity and that driver's bound workspace path; both survive resume |
| `agentId`, `agentSession` | bound external agent and its session handle; absent on provider rooms |
| `agentOptions` | the owner's picks of the agent's session options (option id → value), applied before each turn; cleared when the room binds another runtime |
| `projectId`, `pinned` | owning project (absent = none) and the pin flag |
| `label` | the room's `@handle` for chat channels (`/room @label`); unique across rooms, absent = none |
| `routing` | `replyToOrigin`, `mirrorFinal`, `notifyApprovals` for chat delivery; absent = defaults (true, false, true) |
| `tools` | bound tool catalog: `name`, `description`, `parameters` (raw JSON schema), `enabled`, `permission` (`system`/`agent`/`general`) |
| `usage` | accumulated `inputTokens`, `cacheReadTokens`, `cacheWriteTokens`, `outputTokens` |
| `messageCount` | total logical messages — sealed plus live |
| `activeCount` | messages in the live tail only — the range `Load` returns |
| `sealedSegments` | how many `N.jsonl` files are committed; also the index the next `Compact` writes |
| `createdAt`, `updatedAt` | timestamps; `updatedAt` is restamped on every meta write |

Two things are deliberately absent. There is no `systemPrompt` field — the
prompt ships embedded in the binary, so it is applied in memory and never
persisted (`load.go` ignores stamped copies in old metas for the same
reason). And there is no credential material: `providerId` is just the provider
ID, resolved through the provider repository at use time.

`environment` is required in the current format. A record without it fails
to load; `List` reports invalid metadata rather than silently hiding the
conversation. Existing records must be updated explicitly to a registered
environment before they can be used.

`Save` (`write.go`) upserts this file. It creates the directory on first
write and preserves record-level fields (`createdAt`, the counters) on
update, so a `Save` never disturbs the stream's accounting. The port
contract requires `Save` to precede `AppendMessage` — `AppendMessage`
fails with 404 on a conversation that has no meta.

### The JSONL line stream

Every `*.jsonl` file is a sequence of `lineRecord` envelopes — one JSON
object per line. Two line types share the stream so writes append without
rewriting earlier records, and ordering is literal:

```json
{"type":"message","data":{ …messageRecord… }}
{"type":"tool_output","callId":"call_…","output":"…","outputLatency":"12ms","attachments":[ … ]}
{"type":"delivery","messageId":"msg_…","attachments":[ … ],"receipt":"read"}
```

`type` is `"message"`, `"tool_output"`, or `"delivery"`. A `delivery`
line records a reply's trip to a linked chat on the message it carried:
the files sent with it (kept in the upload store) and the chat's receipt
(`delivered` or `read`). Delivery patches fold onto their message by
`messageId` at read time; files add up without repeats and a receipt
never moves back. A patch whose message is gone (rewritten away) stays
inert. A `tool_output` line carries the
patch fields (`callId`, raw `output`, `outputLatency`, `attachments`) at the envelope level; its
`data` still serializes as an empty message object but is meaningless.
Patches resolve **globally by `callId`** at read time
(`messagesFromLines` in `conversation.go`), so a patch may target a call
sealed in an earlier segment — this is why they are append-only records
rather than in-place edits.

A `message` line's `data` is a `messageRecord`:

| Field | Notes |
|---|---|
| `id`, `role`, `content`, `createdAt` | `role` is `system`/`user`/`developer`/`assistant` |
| `userType` | `origin` (real user input) or `system` (harness-injected) |
| `finishReason` | `stop`/`tool_calls`/`length`/`content_filter` |
| `parts` | Ordered assistant text, reasoning, and tool-call positions, and an agent's records: `plan` (its JSON entries, the latest only), `approval` (`{id, callId, title, action, input, options, optionId}`), `elicitation` (`{id, mode, message, url, action}` — never what the owner entered), and `steer` (input the owner sent into the running agent turn, where the agent took it) |
| `toolCalls` | each with `id`, `name`, `input`, raw `output`, `outputSet`, `outputLatency`, `outputBudget`, `outputAttachments`, `type` (`origin`/`injection`), `callAt`, `optionalContent` |
| `attachments` | `uri` + `mediaType`, optional `name` and `sha256` pin — stored references (`file://`, `https://`), materialized by adapters at send time. Owner uploads point into `uploads/` |
| `references` | the owner's selections in `content`: `{kind, id, label, start, end}`, byte offsets; `kind` is `file`, `folder`, `skill`, `mcp`, `command`, `conversation`, or `project`. Checked against `content` on load; an edit of the text drops them |
| `annotations` | live-page marks the message carried: `{marker, comment, screenshot, descriptor}`; `descriptor.kind` is `element`, `region`, or `page`. Their kept screenshots are the last `attachments`, in order |
| `recording` | the browser recording a user message attached: `{id, status, summary}` — the summary as it read at send time (a live one's meta brief) |
| `optionalContent` | adapter-namespaced extras like `completion.reasoning_content`; domain never interprets values |

Hydration (`decodeMessage`) goes through the public entity constructors
and setters — there is no reconstitute seam — and stored enum strings are
validated strictly. An unknown `role`, `userType`, `finishReason`, or tool
`type`/`permission` fails the load as corrupt or forward-incompatible
data rather than silently coercing.

### Write mechanics

Two primitives cover everything (`conversation.go`):

- `appendLine` opens the file `O_APPEND|O_CREATE`, writes one marshaled
  line, and `fsync`s. This is the hot path — a message or a patch is
  durable in one syscall pair, and concurrent tool results racing to
  persist stay safe because nobody read-modify-writes a stored line.
  Before writing it mirrors `readLines`'s tolerance exactly: it drops a
  final record only when that line fails the same decode/type probe, and a
  complete record that lost its terminator — still decodable, so still a
  record to the reader — is kept, with the missing newline written instead.
  So a new line is never glued onto partial bytes and a tail readers would
  have tolerated is not left behind to turn interior.
- `writeFileAtomic` stages to `<path>.tmp`, `fsync`s, closes, renames over
  the target, then best-effort `fsync`s the directory. A crash mid-write
  leaves the old file or nothing — never a torn target. The file-level
  `fsync` before rename is load-bearing: without it a crash can resurrect
  the name with no contents, and a sealed segment that reads back empty is
  silent history loss rather than an error.

A `sync.Mutex` per repository serializes writers; the store assumes a
single process owns the directory. Every operation additionally holds an
advisory `flock` on `<storage>/conversations/.lock` for its whole
read-modify-write span, so a second instance or process pointed at the
same root serializes instead of tearing staged files or losing meta
updates. The lock is best-effort by platform: it is a real `flock` on
unix, a `LockFileEx` on Windows, and a no-op elsewhere — on those
platforms (or on filesystems like NFS where advisory locks don't bind)
the in-process mutex is the only guard, so the single-owner assumption
still holds.

### `AppendMessage` and `UpdateToolCallOutput`

`AppendMessage` (`append.go`) appends one `message` line to
`active.jsonl`, then bumps `messageCount`/`activeCount` in meta. It
returns `persisted=true` once the line itself is synced, even if the meta
rewrite afterwards fails — the message is already in history; the counter
is only a projection.

`UpdateToolCallOutput` (`updatetool.go`) appends a `tool_output` patch
line carrying raw output, fixed latency, and any media references. Because it is
a pure append with no read-modify-write, parallel tool calls completing
out of order are safe by construction, and a crash mid-batch leaves the
remaining calls honestly pending instead of losing every result.

`RecordDelivery` (`updatetool.go`) appends a `delivery` patch line the
same way; a rewrite that keeps the message re-encodes it with its
`sentFiles` and `receipt` in the `messageRecord`.

### `Compact` — sealing history

`Compact` (`compact.go`) is called after the model has produced a summary
of old context. It seals the first `dropBefore` message lines of
`active.jsonl` **verbatim** into `<sealedSegments>.jsonl`, then rewrites
`active.jsonl` as `[summary line] + surviving message lines + every patch
line`. Patches all move to the tail regardless of position — they stay
global, so a late output still reaches a call that was just sealed.

The commit point is `meta.sealedSegments`: the segment file is written
first, then active is rewritten, then meta is bumped. That ordering is
chosen so the surviving bytes can be recovered — a segment file that exists
but is not counted is an orphan that normal readers reject; if the
active rewrite fails the orphaned segment is deleted (the prefix still
exists in the untouched active copy, so the orphan is a pure duplicate);
if only the meta write fails, the uncounted segment is left on disk as the
sole surviving copy of the dropped prefix, available for offline recovery.
Until metadata and numbered files agree, per-conversation reads and writes
fail rather than presenting a partial history or overwriting
an uncommitted era. Stop every NusaShell process using the store and back up
the conversation directory before recovery. Run
`nusashell conversation repair <id> --discard-duplicate` if the orphaned
segment's message lines still lead `active.jsonl` (the active rewrite did
not land). Run `nusashell conversation repair <id> --commit` if the prefix is
absent from active (the active rewrite landed); this validates the stream
and recalculates both message counters. The command refuses the wrong
action. It supports exactly one extra numbered segment; a missing segment
must be restored from backup. A partially overlapping orphan is not a
documented Compact crash window: neither repair action can safely choose
which copy of a message to keep. If the command rejects both actions, leave
the files unchanged and reconcile them manually from a backup. A
different crash window — an edit interrupted inside sealed history —
carries its own roll-forward verb; see `RewriteFrom` below.

`messageCount` grows by one (the summary is a message) and `activeCount`
shrinks to `activeCount - dropBefore + 1`.

### `RewriteFrom` — editing a message

`RewriteFrom` (`rewrite.go`) is the persistence counterpart of
`Conversation.RewriteFrom` — the `/edit` flow. Editing means replacing one
user message's content and discarding everything recorded after it, so the
caller can regenerate the turn. Two shapes:

- **Target in `active.jsonl`** — the common case. The file is truncated
  right after the edited line, whose `content` is patched in place. Patch
  lines sitting after the cut are kept: they resolve globally by call ID,
  so one may still answer a call in the retained prefix (after `Compact`,
  every patch lives in the tail). Dead patches are harmless.
- **Target in sealed segment N** — repromotion. Segments `0..N-1` and N's
  surviving prefix are merged into the new `active.jsonl`, so the model
  regains the full pre-edit era verbatim. The numbered segments are then
  retired and `sealedSegments` resets to the re-globbed count — the next
  `Compact` starts again at `0.jsonl`. Compaction summaries covering later
  eras would lie about discarded content, so they go away with the old
  tail rather than being preserved.

  The merged stream and the retired eras live in different files, so no
  single rename can commit the edit. The protocol instead stages
  everything around a payload name. First the directory is swept of
  rewrite leftovers (`active.jsonl.rewrite`, `*.jsonl.rewrite-old`) — the
  archive just validated, so anything matching is debris, never something
  to publish. The merged stream is then written durable to
  `active.jsonl.rewrite`, each `k.jsonl` is *parked* as
  `k.jsonl.rewrite-old` — renamed, never deleted — with a directory
  fsync, and only then is `active.jsonl` rewritten with the merged
  bytes. That rename is the commit point: before it the canonical stream
  and every era are intact, after it the merged history is truth and the
  parked files are stale copies. A failure before the commit rolls the
  parking back and drops the payload once every canonical name is
  restored — if a rename-back itself fails, the payload stays because it
  is then the only roll-forward source. That leftover is diagnosed as an
  interrupted edit only when the parked set still has the protocol's shape —
  the contiguous prefix `0.jsonl.rewrite-old … j.jsonl.rewrite-old` with
  every surviving numbered segment an index above it. A gapped or otherwise
  wrong-side leftovers state is refused by `--finish-rewrite` (pinned by
  tests in `repair_test.go`) and needs manual reconciliation from a backup;
  only a properly shaped leftover may roll forward, committing an edit the
  user saw fail — no message is lost (the payload carries the merged stream),
  but the outcome is surprising. After it, `recountMeta` resyncs
  the counters (`sealedSegments` re-globs to zero — a parked name is not
  `*.jsonl`) and the payload and parked files are removed, best effort.

  Two states survive a crash inside that window. A payload plus an
  edit-shaped layout is an interrupted edit: every read fails and names
  the fix, `nusashell conversation repair <id> --finish-rewrite`. The
  failure is store-wide for listing, not just for that room: `readMeta`
  fails for it, so `List` — and with it `GET /api/conversations` — errors
  for the whole store and the sidebar stays empty until the room is
  repaired, the same blast radius the Compact-orphan window has. The
  layout is edit-shaped only when it is one the protocol can leave while
  a payload exists — fewer numbered segments than the metadata counts,
  *and* a parked set that is exactly the contiguous prefix
  `0.jsonl.rewrite-old … j.jsonl.rewrite-old` (segments are parked in
  ascending order and never skipped, so a crash mid-parking leaves a mix
  of parked and numbered files) with every surviving numbered segment an
  index above that prefix — or a payload already byte-identical to the
  live tail, where the publish landed and republishing it is a no-op. A
  payload plus a consistent archive is post-commit debris: invisible to
  readers, swept by the next sealed edit, never republished. The payload
  is only removed once the archive validates, so "inconsistent and no
  payload" is a state this code never produces. Any other mismatch — an
  extra segment, a missing numbered file, a gapped or wrong-side parked
  set, manual damage — is not this window; it keeps the mismatch's own
  recovery guidance.

  `--finish-rewrite` rolls forward, never back: it validates the payload
  (newline-terminated, every line a stream record), publishes it over
  `active.jsonl`, deletes every numbered and parked leftover, recounts
  the counters, and removes the payload. There is no abort — the merged
  stream already carries the only surviving copy of the parked eras, so
  publishing is the only safe completion. Run it with the app stopped; a
  second run, a run against a healthy room, or a payload sitting beside a
  mismatch the edit cannot leave refuses cleanly — under-diagnosis is the
  safe direction, since the only alternative is republishing stale bytes
  over live history.

Both paths re-run `entity.EditableMessage` on the stored
`(role, userType)` tuple — only real user messages (`role=user`,
`userType!=system`) are editable; anything else is a 400, and an ID that
is nowhere in history is a 404. After surgery `recountMeta` resyncs the
counters from the actual stream. There is no replay-record alternative by
design: an append-only "edit" line would leave the stale suffix visible to
the model.

### `Delete`

`Delete` (`delete.go`) is `os.RemoveAll` on the conversation directory —
meta, live tail, every sealed segment, and `tool-output/` spill files go together.
Spill files are retained until the user deletes the conversation. A missing
directory is a 404.

### Two read paths

The port (`infrastructure/domain/repository/conversation.go`) splits reads
by consumer:

- **`Load`** returns the aggregate a turn runs against: meta plus *only*
  `active.jsonl` — the live context the model actually sees. Sealed
  history stays on disk.
- **`Info` / `List` / `LoadAt`** are projections for UIs. `Info` is a
  meta projection with a sealed-filename consistency check; it parses no
  messages. `List` reads every
  conversation's meta, skips directories without a readable one, and
  sorts by `updatedAt` descending. `LoadAt(start, end)` slices the global
  logical history — sealed segments in numeric order, then the tail —
  with clamped indexes and negative-from-end semantics (`LoadAt(id, -10,
  0)` reads the latest 10), backing history browsers and pagination.

When a load includes sealed segments, only files numbered below
`meta.sealedSegments` qualify (`loadMessages`): because `Compact` writes
the segment file before bumping the count, a crash can leave a numbered
file whose prefix the intact active copy still holds — replaying it would
duplicate history, so meta is the authority on what is committed.

Torn-tail tolerance is the other recovery rule (`readLines`): a final
line that fails to decode or has no `type` is dropped, since that is the
signature of a crash mid-append. The writer mirrors that tolerance exactly,
not a narrower one: before appending, `appendLine` probes the true final
line with the same decode/type test. A line that fails it is dropped, but a
record that decodes and carries a `type` is kept even when its terminator
was lost — a missing terminator is not damage to the record, and `readLines`
would have kept it, so the file gains the newline instead of losing the
record. Only that final line is eligible, and the probe is not capped: a
long decodable line is exactly the tail the reader keeps, so the probe reads
it whole. A damaged tail left in place becomes interior corruption on the
next append, after which every read of the room fails; corruption earlier in
the file is reported, not swallowed.

## Providers

Providers are few and operator-configured rather than user-generated, so
`infrastructure/repository/provider/` stores one JSON document per provider
at `providers/<id>.json`. `Save` is atomic — unique temp file via
`os.CreateTemp` (two processes writing the same ID cannot truncate each
other's staged bytes), `fsync`, rename, dir sync — and `List` is a
directory glob. The directory is created `0700` and files `0600` because
records may carry credential references.

### Record format

`providerRecord` (`provider/record.go`):

```json
{
  "id": "openai",
  "name": "OpenAI",
  "style": "completion",
  "endpoint": "https://api.openai.com/v1",
  "apiKey": "secret:provider/openai/api_key",
  "stream": true,
  "models": [ … ]
}
```

`style` is the wire family (`completion`, `responses`, `messages`,
`generatecontent`).
`models` is the catalog; each `modelRecord` mirrors every `Model` field —
`id`, `providerId`, the wire-surface override `style` (empty = inherit the
provider's), the generation parameters (`temperature`, `topP`, `topK`,
`maxOutputTokens`, `presencePenalty`, `frequencyPenalty`, `seed`,
`stopSequences`, `effort`), and catalog metadata (`modelType`,
`contextWindow`, `outputLimit`, `maxImages`, `name`, `canonical`, `aliasOf`,
`supportedEfforts`, `pricing` with per-million `inputPerM`/`outputPerM`/
`cacheReadPerM`/`cacheWritePerM`, `inputModalities`, `outputModalities`,
`optionalContent`). Numeric parameters serialize as pointers' values and
are `omitempty` — unset means "don't send", not zero. Hydration again goes
through constructors and setters with strict enum parsing, so a catalog
entry round-trips losslessly or fails loudly.

### Credentials are references, never plaintext

The `apiKey` field never holds raw key material once a record goes through
the normal write path. The scheme (`infrastructure/domain/valueobject/
keyref.go`) is two prefixes:

- `"env:NAME"` — resolved at `Load` time via `os.Getenv`. An unset
  variable fails the load rather than resolving to `""` — a silent empty
  key would surface as an upstream 401 with no local explanation. `.env`
  in the working directory feeds this, loaded at boot before config.
- `"secret:<key>"` — resolved through the injected `SecretStore`. The
  canonical key a provider's credential vaults under is
  `provider/<id>/api_key` (`drepo.ProviderSecretKey`), shared by the
  repository and the management plane so both write the same vault entry.

When a literal key is supplied — `nusashell provider add … --key-stdin`, or a
direct `Save` — the application vaults it under the canonical name and the
record stores only its `secret:` reference. With no `SecretStore` wired, a
literal is **refused** outright: persisting a key into JSON is treated as
a deployment defect, not a fallback. An already-formed `env:`/`secret:`
ref is persisted verbatim, and `""` clears the credential. The upsert
orders its writes vault-first, record-second — an orphaned vault value is
harmless garbage while a dangling `secret:` ref is a broken provider — and
deletes the canonical vault entry once a record stops naming it (a switch
to `env:` or a foreign ref would otherwise leak the old material).

`Delete` removes `<id>.json` plus the canonical vault entry. Conversations
that referenced the provider keep a dangling ID; resolution failure at send
time is the application's fallback.

### Reads: `Load` vs `Inspect`

Like conversations, providers have two read paths split by consumer:

- **`Load` / `List`** are the live path — the record hydrated with the
  credential *resolved* into key material, ready to send against. `List`
  skips records that fail to load (e.g. a dangling `secret:` ref) since it
  backs model pickers.
- **`Inspect` / `InspectList`** are the management-plane reads — the
  aggregate hydrated with *no* key material, plus `ApiKeyRef` carrying the
  raw stored value (a ref, a literal, or `""`). This is what lets an
  update carry the ref forward instead of pinning whatever it resolved
  to, and what lets `provider show` render a record whose credential is
  unresolvable — marked broken rather than hidden. A file that cannot even
  decode still surfaces as a snapshot with `Provider: nil` and a
  best-effort `ApiKeyRef`, so a repair upsert can clean up the vaulted
  material it points at. Only a truly absent ID is 404.

## Application database

`nusashell.db` is opened by `infrastructure/repository/store.Open`: one
connection, `busy_timeout` 5 s, `foreign_keys` on, WAL journaling, and the
file clamped to `0600` (the `-wal`/`-shm` sidecars inherit its mode). The
schema lives in `migration/NNNN_description.sql`, embedded into the binary.
`Open` applies every file whose number exceeds the database's
`PRAGMA user_version`, each in its own transaction, then records the new
version. A database whose version is newer than the binary knows is
refused rather than run against an unknown schema. A schema change is a
new numbered file; a shipped file never changes.

| Table | Contents |
|---|---|
| `owner` | one row: the bcrypt hash of the owner password |
| `sessions` | login sessions: SHA-256 of the cookie token, the CSRF token, created / last-seen / expiry times (unix seconds) |
| `projects` | projects: id, name, `folders` (JSON array of absolute paths, first = primary), pinned, created / updated times (unix nanoseconds, compared exactly to refuse stale edits) |
| `channel_links` | which room a chat talks to: key (`channel`, `chat_id`, `thread_id`) → `conversation_id`, display name, group flag. A chat has one room; a room may have many chats |
| `channel_inbound` | (`channel`, `message_id`) of messages already taken, so a redelivered message runs once; pruned after a week |
| `channel_settings` | one JSON document per key: each channel's settings (allowlists, group policy, receipts) and the Telegram update offset (`telegram.offset`) |
| `automations` | scheduled prompts: name, prompt, runtime (`agent_id` or `provider_id` + `model_id`), schedule (`schedule_kind` `once`/`cron`, `cron`, `run_at`, `timezone`), `thread` (`new`/`same`) and the kept `conversation_id`, `project_id`, result chat (`notify_channel`, `notify_chat`, `notify_thread`), `expires_at`, `enabled`, `next_run_at`, `version`, created / updated times. Times are unix nanoseconds, `0` = unset. `version` grows on every write and guards claims and saves against each other; `updated_at` changes only on owner edits and refuses an edit based on a stale read |
| `browser_logins` | logins saved in a browser profile: `profile`, `origin`, `username` (unique together), created / updated times. Passwords live in `secrets.db` under `browser-login/<id>` |
| `mcp_servers` | the owner's MCP servers: `name` (unique), `transport`, `command`, `args` (JSON), `url`, `enabled`, created / updated times. Environment and header values live in `secrets.db` under `mcp/<id>` |
| `approval_rules` | a room's "always allow" answers: `conversation_id`, `agent_id`, `action` (the tool action kind; unique with the room), created time. Deleting a room deletes its rules |
| `chat_messages` | messages a channel account saw or sent, for search: (`channel`, `chat_id`, `message_id`) unique, sender id and name, chat name, `at_ms`, from-me and group flags, `text`, `raw` (the channel's own payload, protojson for WhatsApp), `is_live` (a history-sync copy never replaces a live one), `explicit` (sent by `nusashell channel … send`; votes on such a poll reach the room as text), `receipt`. An integer `id` keeps the FTS5 index `chat_messages_fts` (over `text`, kept by triggers) stable. A revoke clears `text`/`raw`; `retentionDays` drops old rows daily |
| `automation_runs` | one row per firing: scheduled / started / ended times, the thread it ran in, `status` (`running`, `completed`, `failed`, `cancelled`, `interrupted`), and a note; the newest 100 per automation are kept, and they are deleted with their automation |

Project membership lives on each conversation's own `meta.json`
(`projectId`), not in this table: a room's record stays complete on its
own, and deleting a project rewrites only its members' metadata.
Deleting an automation keeps the threads its runs wrote; at start, runs
still `running` from a previous process become `interrupted`.

Channel credentials stay out of this database: the Telegram bot token is a
secret-store entry (`channel/telegram/token`), and WhatsApp keeps its
device identity in its own `whatsapp.db` (the whatsmeow store). Deleting a
room deletes its `channel_links` rows.

The session cookie's token itself is never stored, so a copied database
cannot be replayed as a login. Sessions slide: a request more than an hour
after the last renewal extends the expiry to 30 days from then.

## Secret store

`secrets.db` sits at the storage root and implements the `SecretStore`
port (`infrastructure/domain/repository/secret.go`) on SQLite via
`modernc.org/sqlite` — a pure-Go driver, so the binary needs zero cgo.
The schema is one table created at open
(`infrastructure/repository/secret/secret.go`):

```sql
CREATE TABLE IF NOT EXISTS secrets (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
)
```

`Set` upserts (preserving `created_at`), `Get` and `Delete` return the
apperror 404 for absent keys. The handle is capped at one connection and
runs `PRAGMA busy_timeout = 5000` so a short-lived writer like
`nusashell provider add --key-stdin` retries while the server's open store
holds the write lock instead of failing instantly with `SQLITE_BUSY`.
The file is `chmod 0600` — including a pre-existing looser-mode file, which
`New` fixes on open — inside a `0700` parent.

Values are sealed with AES-256-GCM. The 32-byte key sits base64-encoded in
`secrets.key` beside the database (created `0600` on first open, never
overwritten). Each value is stored as base64(nonce ‖ ciphertext) with the
entry's name as associated data, so a value copied onto another name does
not open. A value that does not open under the current key — written
before sealing, or under a lost key — reads as absent (404), so it is set
again like a missing one. The key and the database live side by side: sealing protects a
copied database or backup, not a reader of the whole storage directory.

## Diagnostic log

`logs/` implements the `Logger` port — a write-only diagnostic sink for
provider traffic, kept deliberately separate from conversation history.
`infrastructure/repository/log/` reuses the segment pattern: a live
`active.jsonl` (mode `0600`) plus numbered compressed archives `0.jsonl.gz`,
`1.jsonl.gz`, ….

### Entry shape

Each line is a `record{ts, kind, data}` envelope — timestamp, a grep tag,
and the scrubbed payload. The completion adapter
(`infrastructure/service/core/text/completion/`) emits kinds
`provider.request`, `provider.response` (`request_id`, `model`),
`provider.error` (`status`, `request_id`), and `provider.stream`
(chunk count). Request/response/stream bodies are omitted before persistence.
Append failures are swallowed at the call site — diagnostics must
never break a send.

### The redact pipeline

Before anything reaches the file, `Append` marshals the entry to JSON and
unmarshals it back into `any` — normalizing means `json.RawMessage` or
struct payloads cannot bypass the scrubber — then applies a `Redact`
function per entry. The default is `redact.Value`
(`infrastructure/redact/redact.go`), which deep-walks maps/slices/strings
and replaces matches with `"***"`:

- map keys matching a credential pattern (case-insensitive, tolerant of
  separators): `api[-_]?key`, `access[-_]?token`, `auth[-_]?token`,
  `authorization`, `bearer`, `credential`, `password`, `secret`,
  `x-api-key`, `set-cookie`, `cookie`;
- secret-shaped strings anywhere: `Bearer …` header values, `sk-…` and
  `AIza…` API keys, and `-----BEGIN … PRIVATE KEY-----` PEM blocks.

On top of that, `cmd/nusashell/wire.go` installs a stricter redactor —
`redact.WithoutBodies`: `provider.request` entries are omitted entirely
(the normalized payload has no `body` key but would leak `messages`
content), `redact.Value` runs over the rest, then the `body`
key is deleted from map payloads, chunk lists collapse to `{"chunks": n}`, and
anything else becomes `{"omitted": true}`. The log retains event kinds
and counts without duplicating conversation content.

### Rotation and resume

`log.max_lines` (`LogConfig.MaxLines`, default 2000) rotates
`active.jsonl` once it holds that many records: the file is compressed to
the next numbered `.jsonl.gz` archive (`max+1`) and a fresh active file is opened.
`log.max_files` (`MaxFiles`, default 9) caps sealed archives — the oldest
are deleted past the cap. Zero selects the defaults; `Normalize` clamps
configured values to these maxima.

`New` resumes across restarts: it enumerates existing numbered segments so
numbering continues, and `prepareActive` scans the existing
`active.jsonl` before it is reopened — fail-stop, unlike the
conversation stream's torn-tail-only tolerance. The file is truncated at
the *first* line that is not a complete valid record, so a torn or
corrupt line drops itself and everything after it; the surviving prefix
is then counted so the rotation threshold accounts for prior runs. The
truncation is what lets `O_APPEND` reopen safely — a new record can
never be glued onto partial bytes — and for a diagnostic sink a lost
tail is cheaper than a misread stream. If rotation itself fails, the
logger reopens the active file rather than going permanently dead;
sealed segments stay intact.

## File permissions at a glance

| Path | Mode | Reason |
|---|---|---|
| `nusashell.db` | `0600` | password hash and session records |
| `agents/` and `agents/<id>.json` | `0700` / `0600` | launch environment references |
| `providers/` | `0700` | records may carry credential references |
| `providers/<id>.json` | `0600` | same |
| `conversations/<id>/` and its files | `0755` / `0644` | conversation content, no refs |
| `conversations/<id>/tool-output/*.log` | `0600` | conversation-owned full tool output |
| `logs/active.jsonl` | `0600` | may carry opted-in payloads |
| `secrets.db` | `0600` under `0700` parent | key material, sealed |
| `secrets.key` | `0600` | the sealing key |

## See also

- [Architecture](./architecture.md) — where repositories sit in the layering
- [Domain model](./domain-model.md) — the entities these records hydrate into
- [Adapters](./adapters.md) — who writes the diagnostic log
- [CLI guide](./cli-guide.md) — `provider add/set`, `/compact`, `/edit` from the operator side
- [Extending](./extending.md) — implementing the repository ports against a different backend
- [README](../README.md)
