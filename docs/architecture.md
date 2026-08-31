# NusaShell — architecture

NusaShell is a local, personal AI shell: a Go binary that serves an
embedded vanilla JS/HTML/CSS frontend and brokers conversations with
Messages / Responses / Chat format providers, skills, memory, docs and MCP
plugins. There is no security layer by design (no auth, no rate limiting);
the process listens on `127.0.0.1` by default. Bind it to another address
only on a trusted network (`NUSASHELL_HOST`).

## Layers

```text
frontend/        native ES modules, no build step; embedded via go:embed
transport/       HTTP /rpc/{method...}, WebSocket /ws, static assets
application/     use cases, agent runner, ports, event bus (Bus)
domain/          pure entities and policies (no I/O imports)
contracts/       wire types, method roster, golden JSON fixtures
infrastructure/  jsonstore, sqlitestore, ai adapters, mcpclient, tools, docs, ci
cmd/nusashell/   composition root (env config, wiring, lifecycle)
testdata/        fake stdio MCP server used by handler-level tests
```

The dependency rule points inward: domain imports nothing outside stdlib;
application imports domain + contracts; transport imports application and
contracts; infrastructure implements application ports.

Automation (CI runner + trigger engine) is wired in `cmd/nusashell`: SQLite
`automation.db`, local executor, and a 15s `FireDue` loop. Domain types are
pure; YAML parsing and process execution stay in `infrastructure/ci`. RPC
methods are `ci.*` and `automation.*`. The frontend Automation view is an
embedded ES module with no build step.

## Transports

| Route | Purpose |
| --- | --- |
| `POST /rpc/{method...}` | request/response commands and queries — the method is encoded in the URL path (dots → slashes, e.g. `/rpc/agent/conversations/list`), body is `{method, payload}` → `{ok, result|error}` |
| `GET /ws` | bidirectional: `{id, method, payload}` requests with `{id, ok, ...}` replies plus the event stream as `{type, payload}` |
| `GET /stream?run_id=&message_id=&after=` | per-round SSE stream of live agent deltas (see below) |
| `GET /` + assets | embedded frontend (disk in `NUSASHELL_DEV=1` mode) |

Events are published to an in-memory `application.Bus`; each WS
connection subscribes. High-volume events may be dropped for a slow
subscriber, while turn boundaries, compaction, steer, and auto-continue
lifecycle events stay queued so a state transition cannot be lost behind
deltas. Delivery is ordered per subscriber but has no replay cursor; the
frontend reconciles room state through the conversation and active-turn RPCs
after a race or reload.

Since the round-stream refactor, **live agent deltas do not travel the
WebSocket at all**. Each round (one assistant message, `(run_id,
message_id)`) is staged in an in-memory `application.RoundStreamRegistry`
with a per-stream monotonic `seq`. The frontend opens `GET
/stream?run_id=&message_id=` when `agent.turn.started` fires (or when
re-attaching to a running turn after reload/room switch), receives
`round.delta` frames (`seq`, `kind` = `text` | `reasoning` | `tool`, text),
and closes on `round.done` (`state`, `usage`, `next`). Re-opening with
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
| `agent.*` | `dispatchAgent` | `application/agent_dispatch.go` |
| `ai.*` | `dispatchAI` | `application/ai_dispatch.go` |
| `acp.*` | `dispatchAcp` | `application/acp.go` |
| `plugin.*` | `dispatchPlugin` | `application/plugin_dispatch.go` |
| `skills.*` | `dispatchSkills` | `application/skills_dispatch.go` |
| `memory.*` | `dispatchMemory` | `application/memory_dispatch.go` |
| `learning.*` | `dispatchLearning` | `application/learning_dispatch.go` |
| `docs.*` | `dispatchDocs` | `application/docs_dispatch.go` |
| `settings.*` | `dispatchSettings` | `application/settings_dispatch.go` |
| `logs.*` | `dispatchLogs` | `application/logs_dispatch.go` |
| `telemetry.*` | `dispatchTelemetry` | `application/telemetry.go` |
| `ci.*`, `automation.*` | `handleCI` | `application/ci_handlers.go` |
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

Each conversation has an optional absolute `workspace` path. The frontend
selects it through `agent.conversations.pick-workspace`; the composition root
uses the host folder dialog, so this is a real local path rather than a
browser directory handle. Canceling the dialog leaves the conversation
unchanged.

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
model's available input budget) and 80% of that budget, the provider
summarizes the history non-streaming, the oldest messages are archived,
and a new epoch is written onto the **same conversation ID** (journal,
todos, chunks, and the open room stay attached) via `ResetTranscript`
then `Add`. `agent.compacted` is emitted.

The summarization input is text-only and bounded: media/file attachments are
replaced with a short note (compaction models are often not vision- or
audio-capable, and providers reject media outright — e.g. OpenRouter HTTP 404
"No endpoints found that support image input"), and each tool call's args and
output are truncated to a per-pass cap with an omission marker so a single
oversized tool result (multi-megabyte grep output, 10MB `file_write` content)
cannot exceed the compaction model's context window. After a failed turn the
provider-measured `context_tokens` is cleared so the UI badge cannot display a
stale undercount of the real conversation size.

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
`max_tool_rounds` (1–10000), parallel tool limits, learning review threshold,
an optional dedicated review model (background autolearn), and max
input/output token ceilings. Browser-only preferences such as the default
model, icon-only sidebar, and automatic WebSocket reconnect stay in local
storage because they describe one browser client rather than the local agent
process.

### Prompt caching

Messages-format providers mark the system prompt and tool definitions with
`cache_control: ephemeral`; cache hits appear in `usage.cache_read`.
OpenAI Responses and Chat providers receive a stable `prompt_cache_key`; the
key is 32 ASCII characters and is namespaced as `nusashell_cv_` for normal
conversation turns or `nusashell_bg_` for headless/background review turns.
OpenRouter Chat receives that key plus `session_id` so its provider routing and
Logs → Sessions grouping remain stable. OpenRouter Messages/Responses carry
the same session value in the documented `x-session-id` header. A provider
cache key is a routing hint, not a guarantee of a cache hit.

## Persistence

| Store | Format | Location |
| --- | --- | --- |
| conversations | JSON | `{data}/conversations/<id>.json` |
| providers, skills, plugins, settings | JSON | `{data}/*.json` + `{data}/plugins/` |
| memory, logs | JSONL | `{data}/*.jsonl` |
| API keys | SQLite | `{data}/credentials.db` |

Credentials never touch the JSON/JSONL files. All writes are atomic
(temp + rename). The log file is a bounded ring (2000 entries).

Conversation transcripts are owned by `application.ConversationRepository`.
`NewConversation` is the only constructor for a new room. Compaction keeps
the same conversation ID and starts a new epoch with `ResetTranscript`.
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
browser. Predictable built-ins expose variants such as file-list,
search-results, collection, document, and media; `exec` and `mcp_call` stay
generic terminal views. See
[`decisions/002-tool-presentation-contract.md`](decisions/002-tool-presentation-contract.md).

## Verification baseline

```text
make check   # gofmt + go test -race + go vet + go build
```

Handler-level tests in `transport/` drive the real HTTP/WS/SSE handlers
against a scripted fake LLM server and a fake stdio MCP binary
(`testdata/fakemcp`), covering the full turn lifecycle, tool calls,
compaction, stop, and both provider wire formats. The provider adapters
are ported from the litellm provider tree (Blocks-based request/response
model with explicit validation) and selected by a single thin adapter that
switches on the provider kind.

## Proposed PWA and offline-first design

The current application requires a running Go backend. The proposed, not yet
implemented PWA shell, local offline data, and backend-recovery design is
recorded in [`decisions/001-pwa-offline-first.md`](decisions/001-pwa-offline-first.md).
