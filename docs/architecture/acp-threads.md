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

Unknown notifications are ignored. Unknown requests receive a JSON-RPC
`-32601 Method not found` error.

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
      spawn("agent", ["acp"])
```

- The desktop creates an `acp` conversation with a `providerId`. It sends
  `acp.run` with the user prompt and a `traceId`.
- `AcpSessionService` starts the session on first `acp.run`, then reuses it for
  subsequent turns in the same conversation.
- Server-to-client requests are forwarded to the desktop over `acp.permission_request`
  and `acp.ask_request` events. The desktop replies with `acp.permission_answer`
  or `acp.ask_answer`.
- Streaming content is published as application events and forwarded to the
  desktop over the existing WebSocket event path.

## Provider registry

The desktop owns the ACP provider catalog. `AcpProviderStore` in the main
process combines built-in manifests (Cursor, Codex, Claude Code, Gemini) with
user-saved overrides for `enabled`, `command`, and `args`. Detection is a shallow
command-on-path check. The registry is separate from the AI provider registry
because ACP providers are not OpenAI-compatible API endpoints; they are
executable agents.

## Error handling

- `ACP_PROVIDER_FAILED` — the provider could not be spawned or the JSON-RPC
  handshake failed.
- `ACP_SESSION_NOT_FOUND` — a cancel/answer was sent for a conversation with no
  active session.

## Future work

- Render plan steps as a checkable card in the desktop thread.
- Expand permission and ask cards with the ACP-specific branding.
- Support ACP attachments and file references.
- Implement the `fs` and `terminal` client capabilities so ACP providers can
  ask NusaShell to read/write files or run terminal commands through the
  existing MCP bridge.
