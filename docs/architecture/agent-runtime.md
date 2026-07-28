# Agent runtime

## Objective

NusaShell runs durable, bounded AI conversations whose only executable
capabilities come from MCP. The shell remains the broker: providers never
receive an MCP transport, process handle, credential, or plugin UI channel.

## Runtime flow

```text
conversation JSON (Electron main)
  -> Agent composer rebuilds context from the durable checkpoint
  -> agent.run (WebSocket command)
  -> RunAgentTurnHandler -> InProcessAgentTurnWorker
  -> AgentTurnRunner
     -> compact older context when the configured threshold is exceeded
     -> RoutedAgentProvider -> selected/pinned provider adapter
     -> McpAgentToolGateway -> PluginRuntimeManager -> MCP client
  -> agent.text_delta events + response/checkpoint/trace metadata
  -> Electron main persists the assistant message/checkpoint
```

The turn loop is provider-agnostic. Provider-family definitions normalize
OpenRouter, OmniRoute, 9Router, OpenAI, Claude, and custom connections; the
infrastructure adapter maps their selected dialect (`chat`, `responses`, or
`messages`) to its wire format. Model catalog metadata drives context/output
limits, tool availability, image support, and reasoning effort. Tool calls are validated against the schemas
advertised for that exact round and execute only through
`PluginRuntimeManager`. A reasoning-only/empty provider response receives one
semantic nudge on the next bounded round; a second empty result becomes an
explicit runtime response instead of an opaque failed turn.

Native JSON tool calls are preferred. A bounded parser also recovers fenced
function XML, Anthropic-style `<invoke>` blocks, and Kimi tool-use text when a
compatible gateway serializes a call as content. An identical tool request is
executed once, nudged on its second appearance, and stops the loop on its third.

## Conversations and failure recovery

- Conversations are stored in Electron `userData/agent-conversations.json`
  using serialized mutations and atomic rename.
- The first user message creates a short deterministic title; the conversation
  list is newest-first and deletions require explicit confirmation.
- A failed provider turn does not persist a fake assistant message. The
  unanswered user message remains durable and the UI exposes **Retry turn**.
- Renderer-only working/error bubbles disappear after reload; durable user and
  assistant messages remain the source of truth.
- SSE text deltas update the current working bubble only. `agent.cancel`
  aborts provider HTTP, retry waits, and active MCP calls by trace ID. Partial
  text from a cancelled turn is not persisted.
- User messages may persist up to four images/PDFs, each at most 4 MiB. The
  wire contract accepts bounded data URLs only; remote attachment URLs and
  arbitrary filesystem paths are rejected.

## Context compaction

Before a provider round, the runner estimates input size as `chars / 4`. When
it exceeds `max input tokens - reserve tokens` (with a 1,000-token floor), it:

1. preserves the configured number of recent user turns, including the latest;
2. asks the selected provider for a concise checkpoint of older messages;
3. falls back to a bounded extractive checkpoint if that request fails;
4. returns the checkpoint so Electron can persist its absolute message offset.

Opening the conversation later sends the saved summary plus only messages
after that offset. Recompaction replaces the previous summary and advances the
absolute checkpoint without duplicating already-compacted messages.

## Provider retry

All supported provider dialects use one bounded retry policy in the shared
HTTP adapter. Connection failures and HTTP `408`, `409`, `413`, `425`, `429`,
and `500`–`504` are transient. Other 4xx responses fail immediately.

Backoff is exponential with bounded jitter. `Retry-After` delta-seconds or
HTTP-date overrides the calculated delay but is still capped. One router-owned
attempt budget spans retries and failover candidates. Successful providers are
pinned for later tool rounds, but a transient failure can still move the turn
to the next enabled provider. Auth and validation failures never fail over.

## Configuration

Environment is currently the process-level runtime boundary:

| Variable | Default | Purpose |
| --- | ---: | --- |
| `NUSASHELL_AI_STUB` | `false` | Enable the deterministic test provider. It is never listed in production UI. |
| `NUSASHELL_AI_PROVIDER` | empty | Initial provider slot ID |
| `NUSASHELL_AI_MODEL` | empty | Initial model ID |
| `NUSASHELL_AI_BASE_URL` | empty | Initial provider base URL |
| `NUSASHELL_AI_API_KEY` | empty | Initial API key; never returned or logged |
| `NUSASHELL_AI_MAX_TOOL_ROUNDS` | `8` | Maximum provider/tool rounds |
| `NUSASHELL_AI_STRATEGY` | `failover` | `failover`, `round-robin`, or selected-provider `switch` |
| `NUSASHELL_AI_TOTAL_ATTEMPT_BUDGET` | `4` | Shared retry/failover attempt ceiling per provider round |
| `NUSASHELL_AI_STREAM` | `true` | Request SSE where the provider dialect supports it |
| `NUSASHELL_AI_VISION` | `auto` | `auto`, `on`, or `off` image-pixel gate |
| `NUSASHELL_AI_TIMEOUT_MS` | `60000` | Provider request deadline |
| `NUSASHELL_AI_RETRY_ATTEMPTS` | `4` | Total HTTP attempt budget |
| `NUSASHELL_AI_RETRY_BASE_DELAY_MS` | `250` | First exponential backoff step |
| `NUSASHELL_AI_RETRY_MAX_DELAY_MS` | `5000` | Backoff and Retry-After ceiling |
| `NUSASHELL_AI_RETRY_JITTER` | `0.2` | Backoff jitter fraction |
| `NUSASHELL_AI_CONTEXT_COMPACTION` | `true` | Enable context compaction |
| `NUSASHELL_AI_CONTEXT_MAX_INPUT_TOKENS` | `12000` | Estimated input ceiling |
| `NUSASHELL_AI_CONTEXT_RESERVE_TOKENS` | `3000` | Output/tool reserve |
| `NUSASHELL_AI_CONTEXT_RECENT_TURNS` | `4` | Raw user turns retained |
| `NUSASHELL_AI_CONTEXT_SUMMARY_MAX_CHARS` | `12000` | Checkpoint character bound |

Provider connections, imported models, selected model, and effort are persisted
through the dedicated Electron provider registry. API keys use Electron
`safeStorage`; the renderer receives only masked availability.

## System prompts

`RunAgentTurnHandler` loads prompt files from `resources/agent/prompts/` via
`FilesystemPromptLoader` and injects them before conversation messages reach the
runner. The injection point is the application layer (backend), not the renderer.

| File | Role | Template vars |
| --- | --- | --- |
| `system.md` | Agent identity, product context, what the agent can do | No |
| `mcp-tools.md` | Progressive MCP tool workflow: discovery → grant → call | No |
| `developer.md` | Runtime context: date, environment, available meta-tool names | Yes |
| `compact.md` | Compaction instruction for the checkpoint LLM call | No |

`developer.md` is the single injection surface for `{{current_date}}`,
`{{environment}}`, and `{{available_tools}}` template variables. Static prompts
are injected as-is. Compaction summary messages from prior turns are preserved
between the developer prompt and user messages. Non-summary system messages from
the conversation are dropped to avoid duplicate or stale instructions.

If the prompt loader fails (missing files, I/O error), the handler logs a
warning and sends the raw conversation messages without injected prompts.

The compaction prompt (`compact.md`) replaces the previously hardcoded
compaction instruction string in `AgentTurnRunner`. If the file is absent, the
runner falls back to the built-in default.

## Stability boundary

Tools, prompts, resources, resource templates, completion, and logging are the
stable MCP surface used by this phase. Elicitation and other evolving MCP
capabilities remain documented in `progressive-mcp-tools.md` but are not
silently exposed to the model until their protocol and consent semantics are
stable.
