# NusaShell Light — architecture

NusaShell Light is a local, personal AI shell: a Go binary that serves an
embedded vanilla JS/HTML/CSS frontend and brokers conversations with
Messages / Responses / Chat format providers, skills, memory, docs and MCP
servers. There is no security layer by design (no auth, no rate limiting);
run it on localhost or a trusted network only.

## Layers

```text
frontend/        native ES modules, no build step; embedded via go:embed
transport/       HTTP /rpc, SSE /events, WebSocket /ws, static assets
application/     use cases, agent runner, ports, event bus (Bus)
domain/          pure entities and policies (no I/O imports)
contracts/       wire types, method roster, golden JSON fixtures
infrastructure/  jsonstore, sqlitestore, ai adapters, mcpclient, tools, docs
cmd/nusashell/   composition root (env config, wiring, lifecycle)
testdata/        fake stdio MCP server used by handler-level tests
```

The dependency rule points inward: domain imports nothing outside stdlib;
application imports domain + contracts; transport imports application and
contracts; infrastructure implements application ports.

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
   any requested tool calls (up to 8 rounds).
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
wire format. Attachment byte signatures and data URL media types are
validated at the application boundary.

The composer presents an estimated context counter based on the persisted
conversation and the selected model's `context` window. It is a UI estimate,
not provider-reported token accounting; the exact request usage remains in
the assistant turn metadata.

### Compaction

When the conversation's estimated tokens exceed
`settings.compaction_threshold` (default 40000), the provider summarizes the
history non-streaming, the summary replaces the oldest messages behind a
marker, and `agent.compacted` is emitted.

### Prompt caching

Messages-format providers mark the system prompt and tool definitions with
`cache_control: ephemeral`; cache hits appear in `usage.cache_read`.
OpenAI-compatible endpoints have no standard caching knob, so the flag is a
no-op there.

## Persistence

| Store | Format | Location |
| --- | --- | --- |
| conversations | JSON | `{data}/conversations/<id>.json` |
| providers, skills, mcp servers, settings | JSON | `{data}/*.json` |
| memory, logs | JSONL | `{data}/*.jsonl` |
| API keys | SQLite | `{data}/credentials.db` |

Credentials never touch the JSON/JSONL files. All writes are atomic
(temp + rename). The log file is a bounded ring (2000 entries).

## Tools

Built-in: `skill_list`, `skill_run`, `memory_save`, `memory_search`,
`memory_list`, `memory_delete`, `docs_search`, `docs_read`. Each enabled MCP
server contributes `mcp__<server>__<tool>` tools with the server's own
schemas; stdio connections are lazy and cached per process.

## Verification baseline

```text
make check   # gofmt + go test -race + go vet + go build
```

Handler-level tests in `transport/` drive the real HTTP/WS/SSE handlers
against a scripted fake LLM server and a fake stdio MCP binary
(`testdata/fakemcp`), covering the full turn lifecycle, tool calls,
compaction, stop, and both provider wire formats.
