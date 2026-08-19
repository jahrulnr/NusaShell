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
transport/       HTTP /rpc, SSE /events, WebSocket /ws, static assets
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
| `POST /rpc` | request/response commands and queries (`{method, payload}` → `{ok, result|error}`) |
| `GET /events` | SSE server-to-client event stream, reconnect-friendly, 15s heartbeats |
| `GET /ws` | bidirectional: `{id, method, payload}` requests with `{id, ok, ...}` replies plus the same event stream as `{type, payload}` |
| `GET /` + assets | embedded frontend (disk in `NUSASHELL_DEV=1` mode) |

Events are published to an in-memory `application.Bus`; each SSE/WS
connection subscribes. Subscribers that fall behind drop events rather than
stall the agent loop. One transport per function: the frontend receives
event triggers over WebSocket only and talks to the backend over HTTP
`/rpc`; the SSE endpoint stays available for non-browser clients. The
transports speak the same event vocabulary (`contracts`).

## Agent turn flow

1. `agent.turns.start` validates the conversation, message, and model, then
   persists the user message and an assistant placeholder.
2. A goroutine runs the turn: compaction check → tool list refresh → stream
   rounds. Each round streams into its own assistant message and executes
   any requested tool calls, capped by `settings.max_tool_rounds` (default 8).
3. Deltas and tool lifecycles are pushed as `agent.message.delta`,
   `agent.tool.started`, `agent.tool.completed`, then `agent.turn.done` (or
   `agent.turn.error` / interrupted state).
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
types are validated at the application boundary. HTTP `/rpc` accepts bodies
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
`settings.compaction_threshold` (default 40000) and 80% of the model's
context window, the provider summarizes the history non-streaming, the
summary replaces the oldest messages behind a marker, and `agent.compacted`
is emitted.

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
partial message remains visible and is marked as failed.

### Runtime settings

`settings.get` and `settings.set` expose the persisted agent runtime knobs:
compaction, prompt caching, and `max_tool_rounds` (1–10000). Browser-only
preferences such as the default model, icon-only sidebar, and automatic
WebSocket reconnect stay in local storage because they describe one browser
client rather than the local agent process.

### Prompt caching

Messages-format providers mark the system prompt and tool definitions with
`cache_control: ephemeral`; cache hits appear in `usage.cache_read`.
OpenAI-compatible endpoints have no standard caching knob, so the flag is a
no-op there.

## Persistence

| Store | Format | Location |
| --- | --- | --- |
| conversations | JSON | `{data}/conversations/<id>.json` |
| providers, skills, plugins, settings | JSON | `{data}/*.json` + `{data}/plugins/` |
| memory, logs | JSONL | `{data}/*.jsonl` |
| API keys | SQLite | `{data}/credentials.db` |

Credentials never touch the JSON/JSONL files. All writes are atomic
(temp + rename). The log file is a bounded ring (2000 entries).

## Tools

The built-in roster comes from `Toolbox.ListTools` and is documented in
`resources/agent/docs/tools.md`. MCP tools are intentionally omitted from the
provider's static `tools[]`; the agent discovers them with `mcp_list`,
`tool_list` or `tool_search`, loads `tool_schema`, then calls the observed
`mcp__<server>__<tool>` name. Stdio connections are lazy and cached per process.

## Verification baseline

```text
make check   # gofmt + go test -race + go vet + go build
```

Handler-level tests in `transport/` drive the real HTTP/WS/SSE handlers
against a scripted fake LLM server and a fake stdio MCP binary
(`testdata/fakemcp`), covering the full turn lifecycle, tool calls,
compaction, stop, and both provider wire formats. Codex server-side
compaction tests compile `testdata/fakecodex` so the subprocess path
runs on Windows as well as Unix.

Codex OAuth tokens are stored under the provider ID (active account) and
under `{providerID}:account:{accountID}` for multi-account failover.
Deleting a provider removes both the active key and every account-scoped
credential. A turn that has stored Codex accounts but finds all of them
rate-limited or circuit-open fails immediately instead of using the
blocked active token.

## Proposed PWA and offline-first design

The current application requires a running Go backend. The proposed, not yet
implemented PWA shell, local offline data, and backend-recovery design is
recorded in [`decisions/001-pwa-offline-first.md`](decisions/001-pwa-offline-first.md).
