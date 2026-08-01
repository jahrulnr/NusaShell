# ACP (Agent Client Protocol) threads

This document describes the NusaShell support for external agent clients that
speak the Agent Client Protocol (ACP) over stdio JSON-RPC.

## Motivation

NusaShell's own agent turns are good for tasks that fit the MCP tool model, but
some tools — notably Cursor — already expose an agent CLI that can drive an
external editor/terminal session. ACP threads let those external agents run
inside the same desktop conversation surface, with NusaShell acting as the host
and the ACP provider as the worker.

## Protocol

ACP is framed as line-delimited JSON-RPC 2.0 over a child process's stdin/stdout.

### Client → server (host → provider)

- `initialize` — handshake, client capabilities.
- `authenticate` — optional, if the provider advertises a supported auth method.
- `session/new` — start a fresh session.
- `session/prompt` — send a user prompt.
- `session/cancel` — cancel the running turn.
- `session/exit` — tear down the session.

### Server → client (provider → host)

- `session/update` notification — streaming content:
  - `agent_message_chunk` / `text_delta`
  - `agent_thought_chunk` / `thought_delta`
  - `tool_call`
  - `tool_call_update`
  - `plan`
  - `session_state`
  - `turn_end`
- `session/request_permission` — user must choose an option.
- `cursor/ask_question` — user must answer a question.
- `cursor/create_plan` — provider proposes a plan; acknowledged by the host.

Unknown notifications are ignored. Unknown requests are dispatched to the
provider's `AcpProviderExtension` (see below); if no extension claims the
method, the host replies with a JSON-RPC `-32601 Method not found` error.

## Architecture

```
Desktop Agent view
  ↕  WebSocket
Backend MessageRouter
  ↕  CommandBus / QueryBus
  AcpSessionService
    AcpPermissionService  — tracks pending permission requests
    AcpAskBridgeService   — tracks pending ask questions
    AcpClientPort         — implemented by AcpJsonRpcClient
      spawn(provider.command, provider.args, { env: { ...process.env, ...provider.env } })
      ↳ resolveAcpExtension(providerId) → AcpProviderExtension
```

- The desktop creates an `acp` conversation with a `providerId`. It sends
  `acp.run` with the user prompt and a `traceId`.
- `AcpSessionService` starts the session on first `acp.run`, then reuses it for
  subsequent turns in the same conversation.
- Server-to-client requests are forwarded to the desktop over `acp.permission_request`
  and `acp.ask_request` events. The desktop replies with `acp.permission_answer`
  or `acp.ask_answer`. Vendor-specific requests (e.g. `cursor/ask_question`,
  `cursor/create_plan`) are dispatched to the provider's `AcpProviderExtension`
  inside `AcpJsonRpcClient.handleServerRequest`.
- Streaming content is published as application events and forwarded to the
  desktop over the existing WebSocket event path.

### Provider extensions

Each ACP provider may register an `AcpProviderExtension`
(`packages/infrastructure/src/acp/extensions/`) that owns vendor-specific
server→client requests. The client resolves the extension once per session via
`resolveAcpExtension(providerId)` and delegates unknown methods to it.

- `CursorAcpExtension` — handles `cursor/ask_question` (forwards to
  `AcpClientSink.askQuestion`) and `cursor/create_plan` (publishes an
  `acp.plan` event).
- `CodexAcpExtension` — no-op placeholder; Codex uses the standard
  `session/request_permission` and `session/update` paths. Reserved for future
  Codex-specific methods.

Extensions keep `AcpJsonRpcClient` free of vendor branches. Add a new provider
extension by implementing `AcpProviderExtension` and registering it in
`extensions/index.ts`.

### Authentication

`AcpJsonRpcClient.startSession` drives the handshake:

1. `initialize` — read `authMethods` from the provider.
2. `authenticate` — sent only when the provider descriptor carries an
   `authMethodId` **and** the provider advertised it. If `authenticate` fails
   (e.g. missing `CODEX_API_KEY`), the client **soft-fails**: logs a warning and
   proceeds to `session/new`. This lets Codex fall back to an existing
   `~/.codex` ChatGPT token without an API key.
3. If `authMethodId` is set but not advertised, the handshake hard-fails with
   `ACP_PROVIDER_FAILED` (the configured auth method is unavailable).
4. `session/new` — open the session.

The AI Providers → ACP Agents card exposes a **Connect** button that runs a
one-shot `acp.probe` (spawn → initialize → optional authenticate → session/new
→ close). The result is persisted on the provider config as `authStatus`
(`connected` | `needs-auth`), `authCheckedAt`, and `authError`. The New ACP
menu only lists providers whose `authStatus` is `connected`, so users cannot
start a thread against an unauthenticated provider.

## Provider registry

The desktop owns the ACP provider catalog. `AcpProviderStore` in the main
process combines built-in manifests (Cursor, Codex, Claude Code, Gemini) with
user-saved overrides for `enabled`, `command`, `args`, `authMethodId`,
`authStatus`, `authCheckedAt`, and `authError`. Detection is a shallow
command-on-path check. The registry is separate from the AI provider registry
because ACP providers are not OpenAI-compatible API endpoints; they are
executable agents.

### Codex manifest defaults

The Codex manifest defaults to running the adapter through `npx`:

```
npx -y @agentclientprotocol/codex-acp
```

This works without a global install (npx downloads the package on first run,
which adds a few seconds to the first Connect). To skip the download, install
globally (`npm install -g @agentclientprotocol/codex-acp`) and set
`NUSASHELL_CODEX_ACP_BIN=codex-acp` in the Electron process env.

The manifest also seeds two spawn env defaults:

- `NO_BROWSER=1` — prevents the Codex CLI from opening a browser during
  `authenticate`; required for headless/desktop usage.
- `INITIAL_AGENT_MODE=agent` — starts Codex in agent mode so it can call tools.

These are merged under `process.env` at spawn time (provider `env` wins on
conflict). The manifest omits a default `authMethodId` so Codex can fall back to
`~/.codex` ChatGPT tokens. To use an OpenAI API key instead, set
`OPENAI_API_KEY` (or `CODEX_API_KEY`) in the process env that launches Electron,
then choose `api-key` in Configure → Auth method.

## Error handling

- `ACP_PROVIDER_FAILED` — the provider could not be spawned or the JSON-RPC
  handshake failed.
- `ACP_SESSION_NOT_FOUND` — a cancel/answer was sent for a conversation with no
  active session.

## Future work

- Render plan steps as a checkable card in the desktop thread.
- Support ACP attachments and file references.
- Implement the `fs` and `terminal` client capabilities so ACP providers can
  ask NusaShell to read/write files or run terminal commands through the
  existing MCP bridge.
- Surface `configOptions` returned by `session/new` as a per-thread settings
  popover (model, sandbox mode, etc.) once Codex/Cursor start advertising them.
