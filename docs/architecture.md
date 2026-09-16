# NusaShell — architecture

NusaShell is a local, personal AI shell: a Go binary that serves an
embedded vanilla JS/HTML/CSS frontend and brokers conversations with
Messages / Responses / Chat format providers, skills, memory, docs and MCP
plugins. The process listens on `127.0.0.1` by default; loopback clients
bypass authentication. Remote access is disabled by default and is enabled
from Settings, where non-loopback clients are gated by device pairing — see
"Pairing and access control" below. `NUSASHELL_HOST` controls only the bind
host. When Remote access is enabled, an unset or loopback host changes to
`0.0.0.0`; an explicit non-loopback `NUSASHELL_HOST` remains authoritative.

## Layers

```text
frontend/        native ES modules, no build step; embedded via go:embed
transport/       HTTP /rpc/{method...}, WebSocket /ws, static assets
application/     use cases, ports, event bus (Bus); agent turn loop in application/agent
domain/          pure entities and policies (no I/O imports)
contracts/       wire types, method roster, golden JSON fixtures
infrastructure/  jsonstore, sqlitestore, ai adapters, mcpclient, tools, docs, automation
cmd/nusashell/   composition root (env config, wiring, lifecycle)
testdata/        fake stdio MCP server used by handler-level tests
pkg/time/        stdlib-only machine-local timestamp wrapper
```

The dependency rule points inward: domain imports no application or
infrastructure code; stdlib-only leaf helpers are allowed. Application imports
domain + contracts; transport imports application and contracts; infrastructure
implements application ports.

## Clock and timestamp policy

Application-generated timestamps have one source: `pkg/time.NewTime`. It
captures the operating system's machine-local timezone (`time.Local`), so a
machine configured for `Asia/Jakarta` emits `+07:00` while another machine
uses its own local offset. Use `Time()` for a `time.Time`, `Epoch*` for numeric
timestamps, and `Format`/`RFC3339`/`DMY` for strings. Existing timestamps are
normalized through the same wrapper when rendered or persisted. A trigger
without an explicit timezone also follows the machine timezone; a configured
trigger's explicit `timezone` remains authoritative for scheduling.

Automation (runner + trigger engine) is wired in `cmd/nusashell`: SQLite
`automation/workflows.db`, local executor, and a 15s `FireDue` loop. Domain
types are pure; YAML parsing and process execution stay in
`infrastructure/automation`. RPC methods are `automation.*`. The frontend
Automation view is an
embedded ES module with no build step.

## Transports

| Route | Purpose |
| --- | --- |
| `POST /rpc/{method...}` | request/response commands and queries — the method is encoded in the URL path (dots → slashes, e.g. `/rpc/agent/conversations/list`), body is `{method, payload}` → `{ok, result|error}` |
| `GET /ws` | bidirectional: `{id, method, payload}` requests with `{id, ok, ...}` replies plus the event stream as `{type, payload}` |
| `GET /stream?run_id=&message_id=&after=` | per-round SSE stream of live agent deltas (see below) |
| `GET /pairing/status` + `POST /pairing/exchange` | public pairing bootstrap routes (remote device poll + one-time challenge exchange); all other `pairing.*` methods are loopback-only RPC |
| `GET /` + assets | embedded frontend (disk in `NUSASHELL_DEV=1` mode) |

### Pairing and access control

`transport.AuthMiddleware` wraps the mux on every startup. A request counts as
local only when **both** halves hold: the effective client IP is loopback and
the request `Host` names a loopback authority (`localhost`, `127.0.0.0/8`,
`::1`). `ClientIP` derives the client proxy-aware — it honors
`X-Forwarded-For`/`X-Forwarded-Proto` only when the immediate peer is loopback,
and takes the rightmost `X-Forwarded-For` entry. The `Host` half is deliberately
fail-closed: a public hostname, a LAN address, the wildcard address, or an empty
`Host` is remote even when the TCP peer is loopback, so a host-local forwarder
that injects no `X-Forwarded-For` cannot impersonate a local caller. A local
alias (a hosts-file name that resolves to a loopback address) counts as remote
too: `Host` is the only signal available, and treating an unrecognized `Host` as
local is exactly the hole this rule closes. Local
requests bypass auth. Non-loopback requests to protected paths (`/rpc/`,
`/ws`, `/stream`, `/local-file`, `/plugins`) require a valid session cookie;
`/healthz` stays public but returns only a minimal `{ok, service}` identity
to remote callers. Public bootstrap routes (`/pairing/status`,
`/pairing/exchange`) plus static assets and `/sounds/` are always allowed so
the pairing page can load.

When remote access is disabled, non-loopback protected requests return
`REMOTE_ACCESS_DISABLED`; the frontend explains that the host must enable
Settings → Remote access. When the setting is enabled and a loopback reverse
proxy or tunnel fronts the core, `ClientIP` trusts
the **rightmost** `X-Forwarded-For` entry — the address the trusted proxy
appended for its own TCP peer. Earlier entries are client-supplied and can
be spoofed (a remote client sending `X-Forwarded-For: 127.0.0.1` must not
become loopback). The deployment requirement: the loopback proxy MUST append
or overwrite `X-Forwarded-For` — the nginx
`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` / Caddy default
behavior. A proxy that passes the client header through verbatim breaks the
trust rule and must not be used; a forwarder that omits `X-Forwarded-For`
entirely is no longer mistaken for a local caller (the `Host` half of the local
rule prevents that), but every client then appears as the forwarder's own peer
address.

Browser-originated calls are same-origin checked as well: `POST /rpc/...` and
the public `POST /pairing/exchange` reject a request whose `Origin` names a
different host or is opaque (`null`), because a local caller bypasses pairing
and a third-party page needs no preflight to reach these routes. Requests
without an `Origin` header are non-browser callers and stay allowed. `/ws`,
`/stream`, and `/local-file` re-check the remote session inside the handler, so
the gate still holds when a handler is exercised without the middleware.

The host-driven flow: Settings → Remote access enables the feature, persists
one or more HTTP(S) addresses remote devices can reach, and creates a one-time challenge
(`pairing.challenge.create`, loopback-only), renders a QR/link client-side,
and the remote device opens the URL, polls `/pairing/status`, and — after
host approval — exchanges the challenge at `/pairing/exchange` for a 30-day
`HttpOnly` `nusashell_session` cookie (Secure over HTTPS, SameSite=Lax;
MaxAge tracks the session TTL). Codes and tokens are stored only as SHA-256
hashes in `pairing.db` (created when remote access is enabled, including for
loopback-bound instances behind a trusted proxy/tunnel).
Failed exchanges are rate-limited per source and the claim is serialized so
a challenge can be consumed exactly once. Paired devices are
listed/revocable via the loopback-only `pairing.sessions.*` RPCs; pairing
management methods are denied to non-loopback callers (a paired remote gets
the normal API, not device-management authority), including over WebSocket
frames, and remote WS upgrades must present a same-origin `Origin`.
Revocation is atomic and cuts live access: remote WS sessions are
re-validated per frame plus a periodic watchdog closes idle-listener
sockets, and remote SSE streams re-check on each ping tick — a revoked
device loses access within seconds, not just on reconnect. The addresses
encoded into QR/links are persisted host-typed configuration — they do not
change where the core listens. One challenge produces one link/QR per
configured address. Changing the enable toggle restarts the backend
automatically so startup wiring and transport policy match the persisted
setting. `app.info` reports the bound `listen_addr` so the Settings UI can
warn (not block — tunnels/proxies are legitimate) when a link cannot be
reached, e.g. a LAN address while the listener is loopback-only, or a
host/port mismatch.

Events are published to an in-memory `application.Bus`; each WS
connection subscribes. High-volume events may be dropped for a slow
subscriber, while turn boundaries, compaction, steer, and auto-continue
lifecycle events stay queued so a state transition cannot be lost behind
deltas. Delivery is ordered per subscriber but has no replay cursor; the
frontend reconciles room state through the conversation and active-turn RPCs
after a race or reload.

Since the round-stream refactor, **live agent deltas do not travel the
WebSocket at all**. Each round (one assistant message, `(run_id,
message_id)`) is staged in an in-memory `application/agent.RoundStreamRegistry`
(aliased as `application.RoundStreamRegistry`)
with a per-stream monotonic `seq`. The frontend opens `GET
/stream?run_id=&message_id=` when `agent.turn.started` fires (or when
re-attaching to a running turn after reload/room switch), receives
`round.delta` frames (`seq`, `kind` = `text` | `reasoning` | `tool`, text),
and closes on `round.done` (`state`, `usage`, `next`). A `kind: "tool"`
start frame also carries the raw `args` and normalized `presentation`; later
chunks for that tool carry only `text`. Re-opening with
`after=<lastSeq>` replays exactly the missed frames (idempotent resume), so
a dropped connection self-heals; `next` chaining carries tool-loop and
auto-continue rounds forward without WebSocket round bookkeeping. The round
is committed to the conversation store atomically when it seals, so a
snapshot read mid-round can never see torn content. The WS keeps signaling:
`agent.turn.started`, `agent.tool.started`, `agent.tool.completed`,
`agent.turn.done`, `agent.turn.error`, steer, ask, compaction, and
non-agent domains. The transports speak the same event vocabulary
(`contracts`).

### RPC dispatch

`App.Dispatch` routes by the first dot-segment of the method to per-domain
dispatchers, each owning its routing table in a separate file:

| Prefix | Dispatcher | File |
| --- | --- | --- |
| `agent.*` | `dispatchAgent` → `conversation` / `agent.Service` | `application/agent_wrappers.go`, `application/agent/`, `application/conversation/` |
| `ai.*` | `provider.Service.Dispatch` | `application/provider/` |
| `acp.*` | `dispatchAcp` | `application/acp.go` |
| `plugin.*` | `plugins.Service.Dispatch` | `application/plugins/` |
| `skills.*` | `skills.Service.Dispatch` | `application/skills/` |
| `memory.*` | `memory.Service.Dispatch` | `application/memory/` |
| `experience.*` | `learn.Service.Dispatch` | `application/learn/` |
| `learning.*` | `learn.Service.Dispatch` | `application/learn/` |
| `docs.*` | `dispatchDocs` | `application/docs_dispatch.go` |
| `settings.*` | `dispatchSettings` → `settings` / `pets` / `media` | `application/settings/`, `application/pets/`, `application/media/` |
| `logs.*` | `logs.Service.Dispatch` | `application/logs/` |
| `telemetry.*` | `telemetry.Service.Dispatch` | `application/telemetry/` |
| `automation.*` | `Automation.Dispatch` | `application/automation/` |
| `app.info` | inline | `application/app.go` |

Adding a new method means: add the constant to `contracts/`, add a case to
the matching domain dispatcher, and add a handler-level test in
`transport/`.

## Agent turn flow

1. `agent.turns.start` validates the conversation, message, and model, then
   persists the user message and an assistant placeholder.
2. A goroutine runs the turn: compaction check → tool list refresh → stream
   rounds. Each round streams into its own assistant message and executes
   any requested tool calls, capped by `settings.max_tool_rounds` (default 8).
   The system prompt and top-level `tools[]` are built from the current Go
   runtime for the new turn; they are not read from or persisted into the
   conversation JSON. Therefore an existing room uses the current binary's
   prompt and tool roster after a backend restart. Persisted hydration may
   still contain older discovery results until the next compaction, but those
   results are context state—not the authoritative top-level `tools[]`.
   Internal delegates additionally project the same incremental transcript
   chunks and terminal lifecycle events as external ACP runs, so the ACP UI
   and a future ACP server can consume one agent-stream boundary.
3. Deltas are staged per round in the in-memory round-stream registry and
   streamed over `GET /stream` as `round.delta` frames; the WebSocket carries
   the lifecycle signals (`agent.turn.started`, `agent.tool.started`,
   `agent.tool.completed`, then `agent.turn.done` or `agent.turn.error` /
   interrupted). The final `round.done` frame carries `next` (the following
   round's `message_id`) for tool loops and auto-continue chains, so the
   frontend chains streams without depending on additional WS delivery.
   Turn terminal and compaction events carry the active run and assistant
   message identity where applicable, so a refreshed client can reattach to
   the current round instead of an earlier assistant message.
4. `agent.turns.stop` cancels the run context; partial output is kept and
   marked `interrupted`.

### Conversation workspace and attachments

Each conversation has an optional absolute `workspace` path on the NusaShell
host. The frontend opens an in-app folder browser that walks server
directories through `agent.workspace.list-dirs` and persists the selection
through `agent.conversations.set-workspace`; the application validates the
path (absolute, existing directory) before storing it. No host dialog is
involved, so workspace selection works from any device, including mobile
browsers over the LAN. Canceling the picker leaves the conversation
unchanged.

Until a workspace is picked, the active workspace defaults to the host home
directory (wired from `os.UserHomeDir()`), so workspace-gated features such
as `memory_project` work from the first turn instead of resolving relative
paths against `.`.

`agent.turns.start` accepts an optional `attachments` array. It supports up
to four attachments per turn, each at most 4 MiB: UTF-8 text (`text/plain`),
PNG/JPEG/GIF/WebP images, and PDF documents. Text is sent as text; binary
attachments are persisted and mapped to each provider's native multimodal
wire format. Attachment UTF-8 validity, byte signatures, and data URL media
types are validated at the application boundary. HTTP `/rpc/{method...}` accepts bodies
up to 64 MiB so four encoded attachments fit the envelope and plugin ZIP
uploads (`plugin.install`) with bundled `node_modules` are accepted.

The composer presents an estimated context counter based on the persisted
conversation and the selected model's `context` window. It is a UI estimate,
not provider-reported token accounting; the exact request usage remains in
the assistant turn metadata. Message size estimates ignore provider usage
totals and do not double-count chronological `steps` against mirrored
content, reasoning, or tool-call fields.

### Compaction

When the conversation's estimated tokens exceed the lesser of
`settings.compaction_threshold` (default 0 = auto, which means 80% of the
model's available input budget) and 80% of that budget, a new epoch is written
onto the **same conversation ID** (todos, chunks, and the open room stay
attached) via `ResetTranscript` then `Add`. `agent.compacted` is emitted.
Client-side compaction summarizes history with a non-streaming request. Codex
instead sends a separate streaming remote-v2 request ending in
`compaction_trigger`, then starts a new epoch with a chronological retained
transcript suffix. The Codex adapter filters that suffix into the provider's
retained user prefix, places the opaque checkpoint after it, and preserves
every later user/assistant/tool item in order. OpenAI Responses may receive an
opaque checkpoint during its normal stream.

The client-side summarization input is text-only and bounded: media/file attachments are
replaced with a short note (compaction models are often not vision- or
audio-capable, and providers reject media outright — e.g. OpenRouter HTTP 404
"No endpoints found that support image input"), and each tool call's args and
output are truncated to a per-pass cap with an omission marker so a single
oversized tool result (multi-megabyte grep output, 10MB `file_write` content)
cannot exceed the compaction model's context window. After a failed turn the
provider-measured `context_tokens` is cleared so the UI badge cannot display a
stale undercount of the real conversation size.

The summarizer advertises exactly one tool, `summary()`, and carries the
contract in its prompt ("output only the handoff checkpoint via the summary
tool", "call the summary tool exactly once") rather than forcing the tool
through `tool_choice`. Forcing a named tool choice is what used to make
compaction fail outright on reasoning providers: with thinking mode enabled the
provider answers HTTP 400 (`Thinking mode does not support this tool_choice`)
before the model is asked, so every pass failed and the turn ended with
`compaction failed: summary too short`. The summary is read from the tool-call
arguments when the model calls the tool, and from the assistant text when it
answers in prose; a pass counts only when the text clears the minimum length
and does not echo the live assistant turn, and a too-short pass is retried with
a doubled token budget.

`settings.compaction_workflow` picks the request shape: `dedicated` (default)
sends a summary-only system prompt and only the `summary()` tool, while `reuse`
keeps the conversation's agent system prompt, its full toolbox, and its prompt
cache prefix. Because the reuse workflow never receives the compaction system
prompt, the **last user message** — the handoff prompt — is the only place the
guardrails can live for both workflows: stop, no task work, no reasoning-only
output, conversation language, tool results treated as data, and the checkpoint
structure. `TestCompactionHandoffGuardReachesEveryWorkflow` fails if a clause or
a workflow loses them.

### Upstream recovery

Before a provider stream has emitted content or reasoning, transient upstream
failures are retried against the same provider up to three total attempts.
Retryable failures are HTTP `408`, `409`, `425`, `429`, and `5xx`, as well as
temporary transport failures. Permanent `4xx` responses (such as invalid
credentials or an invalid request) fail the turn immediately. Backoff starts
at 250 ms, doubles up to four seconds with a small jitter, and never retries
before a provider-supplied `Retry-After` value. Conversation compaction uses
the same policy.

Once a stream has emitted visible content or reasoning, NusaShell saves that
partial assistant message instead of replaying it. It performs at most one
continuation request with the saved history and a prompt to continue without
repeating text. Tool calls are executed only after a complete provider round,
so recovery never reruns a tool; a continuation also does not consume the
configured tool-round budget. When the continuation cannot complete, the
partial message remains visible and is marked as failed. A user Retry with
a different model appends a new assistant after that failed message; formed
message IDs are never deleted.

### Runtime settings

`settings.get` and `settings.set` expose the persisted agent runtime knobs:
compaction (enabled, threshold, optional dedicated model), prompt caching,
`max_tool_rounds` (1–10000), parallel tool limits, an optional dedicated
`review_model` for background learning jobs (consolidator / skill evolver /
evaluator), and max input/output token ceilings. Learning jobs enqueue from
experience signals, not a turn-count threshold. Browser-only preferences
such as the default model, icon-only sidebar, and automatic WebSocket
reconnect stay in local storage because they describe one browser client
rather than the local agent process.

### Prompt caching

Messages-format providers mark the system prompt and tool definitions with
`cache_control: ephemeral`; cache hits appear in `usage.cache_read`.
OpenAI Responses and Chat providers receive a stable `prompt_cache_key`; the
key is 32 ASCII characters and is namespaced as `nusashell_cv_` for normal
conversation turns or `nusashell_bg_` for headless/background learning-job turns.
For a given provider/model/conversation, the key remains stable while the
request contract is unchanged. Its digest also includes the current system
prompt and top-level `tools[]`, so a new binary, changed user instructions,
or an ACP enable/disable cannot reuse a cache/session shard built for the old
contract. The key is recomputed from runtime values at the turn boundary; no
system prompt or tool definition is persisted in the conversation.
OpenRouter Chat receives that key plus `session_id` so its provider routing and
Logs → Sessions grouping remain stable. OpenRouter Messages/Responses carry
the same session value in the documented `x-session-id` header. A provider
cache key is a routing hint, not a guarantee of a cache hit.

## Persistence

| Store | Format | Location |
| --- | --- | --- |
| conversations | JSON | `{data}/conversations/<id>.json` |
| providers, plugins, settings | JSON | `{data}/config/*.json` + `{data}/plugins/` |
| skills | markdown + JSON | `{data}/skills/<id>/` (`SKILL.md`, `meta.json`, `versions/<n>/`) |
| profile documents | Markdown | `{data}/memory/user.md`, `{data}/memory/soul.md` (first boot copies embedded `resources/templates/` when missing) |
| growth catalogs | JSONL | `{data}/growth/{experiences,memories,jobs,operations}.jsonl` |
| learning search/log | JSONL | `{data}/learning/{edges,embeddings,trajectory}.jsonl` |
| logs | JSONL | `{data}/logs.jsonl` |
| API keys | SQLite | `{data}/credentials.db` |

The full tree, including automation and attachments, is in
`resources/agent/docs/data-locations.md`.

Credentials never touch the JSON/JSONL files. All writes are atomic
(temp + rename). The log file is a bounded ring (2000 entries).

Conversation transcripts are owned by `application.ConversationRepository`.
New rooms are frontend drafts only. The first real user turn allocates the
conversation ID and persists the room, user message, and assistant placeholder
as one start transaction; the request's temporary `conversation_key` is an
in-process correlation/idempotency marker and is not persisted. Repository
`Save()` rejects conversations without a real user message, so empty drafts
remain temporary and cannot be made durable through metadata or announcement
paths. Compaction keeps the same conversation ID and starts a new epoch with
`ResetTranscript`.
`GetAll` / `GetFrom(start, end)` / `GetById(id)` read the current room;
`Add(role, args...)` is the only way to grow the transcript; `Save()`
persists and rejects any rewrite, reorder, or shrink of formed message IDs.
jsonstore `ConversationStore.Save` remains the file adapter underneath.

## Tools

The built-in roster comes from `Toolbox.ListTools` and is documented in
`resources/agent/docs/tools.md`. MCP tools are intentionally omitted from the
provider's static `tools[]`; the agent discovers them with `mcp_search`
(query-based, ranked) or `tool_list` (list all tools of a server — both
return the same `ref`-shaped items), then executes them via `mcp_call`
with the observed `ref` — `mcp__<server>__<tool>` names are not callable.
Stdio connections are lazy and cached per process.

Tool transcript data and frontend display data are separate by contract.
`ToolCallDTO` and tool lifecycle events keep raw `args`/`output` for the
provider-facing transcript and add an optional `presentation` view for the
browser. `agent.tools.contracts` exposes the same workspace-sensitive roster
used by `ToolFactory`, including input schemas, versioned CSS identities, and
the normalized request/result shapes. Predictable built-ins expose variants
such as file-list, search-results, collection, document, and media; `exec`
and `mcp_call` stay generic terminal views. See
[`decisions/002-tool-presentation-contract.md`](decisions/002-tool-presentation-contract.md).

## Verification baseline

```text
make check   # gofmt + go test -race + go vet + go build + frontend tests
```

Handler-level tests in `transport/` drive the real HTTP/WS/SSE handlers
against a scripted fake LLM server and a fake stdio MCP binary
(`testdata/fakemcp`), covering the full turn lifecycle, tool calls,
compaction, stop, and both provider wire formats. The provider adapters
are ported from the litellm provider tree (Blocks-based request/response
model with explicit validation) and selected by a single thin adapter that
switches on the provider kind.

## PWA shell

The embedded frontend ships as an installable PWA (`manifest.webmanifest`,
`sw.js`): network-first with cache fallback for shell assets, never for
`/rpc`, `/ws`, `/stream`, `/local-file`, `/sounds`, or `/plugins/*`. When
the Go backend is unreachable, a full-window offline overlay covers every
view. Live agent work still requires the local process.
