# Agent runtime

## Objective

NusaShell runs durable, bounded AI conversations whose only executable
capabilities come from MCP. The shell remains the broker: providers never
receive an MCP transport, process handle, credential, or plugin UI channel.

## Runtime flow

```text
conversation JSON (Electron main)
  -> Agent composer rebuilds context from the durable checkpoint
  -> agent.run (WebSocket command; `resume: true` skips system-prompt injection)
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
- System logs (backend, agent, plugin, MCP) are persisted to
  `userData/logs/nusashell.log` via Pino multistream (stdout + file).
  The file is appended to across restarts and is safe to inspect after a crash.
- The first user message creates a short deterministic title; the conversation
  list is newest-first and deletions require explicit confirmation.
- A failed provider turn does not persist a fake assistant message. The
  unanswered user message remains durable and the UI exposes **Retry turn**.
- When a provider call fails mid-turn after tool work has already accumulated,
  the runner first attempts a **soft recover** — re-calling the provider with
  the same accumulated messages up to `softRecoverAttempts` times (default 1,
  max 3, configurable via `NUSASHELL_AI_SOFT_RECOVER_ATTEMPTS`). Cancellation
  aborts immediately and is never retried.
- If soft recover is exhausted and the turn had progress, the runner throws
  `AGENT_PROVIDER_FAILED` with a `details.partial` snapshot containing the
  accumulated `messages`, `steps`, `toolCalls`, `traceId`, `rounds`, and
  optional `model`/`providerId`/`usage`. The desktop seals the streaming
  message, persists an **interrupted** assistant message (`status:
  "interrupted"`) carrying `resumeMessages`, and still shows the error footer
  with **Retry**.
- **Retry** on an interrupted message calls `agent.run` with `resume: true`
  and the saved `resumeMessages`, skipping system-prompt injection so the
  provider sees the exact mid-turn context. On success the interrupted
  message is replaced with the completed assistant message; on a new
  mid-turn failure the same interrupted message is updated with the new
  partial. If `resumeMessages` was dropped (snapshot exceeded ~512 KiB),
  Retry falls back to a full restart from durable history.
- `buildAgentContext` skips `status: "interrupted"` messages when building
  context for a new turn — interrupted progress lives only in
  `resumeMessages` for the continue path.
- Renderer-only working/error bubbles disappear after reload; durable user and
  assistant messages remain the source of truth.
- SSE text deltas update the current working bubble only. `agent.cancel`
  aborts provider HTTP, retry waits, and active MCP calls by trace ID. Partial
  text from a cancelled turn is not persisted.
- User messages may persist up to four images/PDFs, each at most 4 MiB. The
  wire contract accepts bounded data URLs only; remote attachment URLs and
  arbitrary filesystem paths are rejected.

## Parallel tool rounds

When a provider round emits multiple tool calls, the runner executes them
**concurrently** by default — not sequentially. This applies to calls that
target different plugins or independent I/O; same-plugin calls naturally
serialize through the per-plugin `PluginOperationQueue` inside
`McpAgentToolGateway`.

- **Segmentation:** the batch is split into contiguous parallel-safe runs and
  standalone **barrier** segments. Barrier tools (currently `ask_question`)
  must run alone, in order — they block the turn for user input and cannot
  overlap siblings. Non-barrier neighbors form one parallel segment.
- **Bounded pool:** parallel segments run through a tiny worker pool capped at
  `maxConcurrentToolCalls` (env `NUSASHELL_AI_MAX_CONCURRENT_TOOL_CALLS`,
  default **8**, clamp 1–32). `maxConcurrentToolCalls: 1` is a full sequential
  escape hatch.
- **Order preservation:** `onToolCallStart` fires for all calls in a segment
  up front (the UI shows the full batch immediately). Results are collected
  indexed by original call order and appended to `messages`/`steps` in that
  order regardless of completion order.
- **Cancel mid-batch:** if the abort signal fires, in-flight calls drain via
  `cancelTurn` / MCP cancel. Any slot still without an execution is filled
  with a cancelled stub (`{ ok: false, error: "Tool call cancelled" }`) and
  `onToolCallEnd` is emitted so the UI seals every card. Every `tool_call_id`
  in the assistant message gets a tool result — siblings are never dropped.

## Stream reliability

Agent and ACP streaming events carry a **per-traceId `streamSeq`** — a
monotonic integer starting at 1, assigned at the application publish site
(`StreamSeqRegistry` in `container.ts` / `AcpSessionService`). The WS
transport stays a dumb broadcaster; it copies `streamSeq` into the event
payload but does not generate it. The counter is cleared when a turn ends.

### Turn lifecycle events

| Event | When | Payload |
| --- | --- | --- |
| `agent.turn_started` | Before the runner starts the first provider round | `traceId`, `streamSeq` |
| `agent.turn_end` | After the turn settles (completed / cancelled / failed / superseded) | `traceId`, `reason`, `streamSeq` |
| `agent.cancel_requested` | User clicks Stop; `cancel-agent-turn` command received | `traceId`, `streamSeq` |
| `agent.turn_superseded` | A new turn supersedes an in-flight one via `supersedeTraceId` | `traceId` (old), `byTraceId` (new) |

`agent.cancel` returns immediately with `phase: "requested"`. The UI does
**not** assume the turn is sealed at that point — it waits for
`agent.turn_end` (with a 2-second fallback timeout) before sealing streaming
tool cards and the streaming message. This prevents the "card stuck in
running" state when in-flight MCP calls take time to drain after cancel.

### Supersede

`agent.run` accepts an optional `supersedeTraceId`. When set, the handler
cancels the old trace via `AgentTurnCoordinator.cancel()` and emits
`agent.turn_superseded` so the UI can mark the old turn as superseded. The
old turn's `onTurnEnd` fires with `reason: "superseded"`.

### Desktop sequence gate

The renderer wraps streaming event handlers in a `createStreamSeqGate()`
(`stream-seq-gate.js`). The gate:

1. **Drops stale events** — `streamSeq <= lastSeen` for the same `traceId`
   is silently dropped (prevents out-of-order rendering from late events).
2. **Flags gaps** — `streamSeq > lastSeen + 1` is accepted but the gate
   calls `onStreamGap(traceId, streamSeq)` so the presenter can mark the
   turn incomplete.
3. **Accepts non-streaming events** — events without `streamSeq` pass
   through unchanged (legacy/plugin events are unaffected).

### Incomplete tool card sealing

`tool_call_start` creates a **skeleton** tool card in the presenter. The
card is only sealed (success/error state, output rendered) when the matching
`tool_call_end` arrives. If `turn_end` fires while any card is still in the
`is-running` state, `sealStreamingToolCardsIncomplete()` marks those cards
as incomplete (`is-incomplete` / `is-error` class, "Tool call did not
complete" output) so the UI never leaves a spinning card behind.

### WS-edge redaction

Before an event envelope or error response crosses the WebSocket boundary,
the transport mapper redacts likely-sensitive values from:

- **Tool call args** — object keys matching `password`, `token`, `apiKey`,
  `secret`, `bearer`, `credential`, etc. are replaced with `[REDACTED]`.
- **Tool output and error strings** — `Bearer <token>`, `Authorization:
  <scheme> <value>`, `sk-…` API keys, and long base64-like tokens are
  scrubbed.
- **Error details** — `ApplicationError.details` (structured context) is
  recursively redacted before being sent to the client.

This is defense-in-depth; the application layer should also avoid emitting
secrets, but the WS mapper is the last choke point before data reaches the
renderer.

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
| `NUSASHELL_AI_SOFT_RECOVER_ATTEMPTS` | `1` | Mid-turn soft recover retries after a provider call fails with tool progress already accumulated (0–3) |
| `NUSASHELL_AI_MAX_CONCURRENT_TOOL_CALLS` | `8` | Maximum concurrent tool executions within a parallel segment (1–32; 1 = sequential) |
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

## Documentation tools

The agent can search and read an internal Markdown corpus located in
`resources/agent/docs/` through shell-owned meta-tools:

- `docs_search` — lexical keyword search returning scored chunks.
- `docs_list` — lightweight catalog of all indexed documents.
- `docs_read` — full document or single chunk read, with `max_chars` and
  `offset` pagination.

`MarkdownDocsIndex` in the infrastructure layer walks `docsRoot`, builds an
index JSON in `docsIndexStorageRoot`, and caches it in memory. The index is built
lazily on first query if it is not already ready. The gateway returns structured
envelopes (`{ ok, data, meta }`) with `meta.data_is_untrusted: true` so the model
treats the returned text as reference material, not privileged instructions.

The `ui/` subdirectory is generated from `resources/agent/docs/ui-source/ui-map.json`
at build time by `pnpm scan:ui-docs` (also run as a `prebuild` hook). It
contains one Markdown file per NusaShell view and describes the purpose of each
view, how to open it, and every control or interaction within it. Agents should
search this corpus first when the user asks how to navigate NusaShell or use a
specific UI element.

## Stability boundary

Tools, prompts, resources, resource templates, completion, and logging are the
stable MCP surface used by this phase. Elicitation and other evolving MCP
capabilities remain documented in `progressive-mcp-tools.md` but are not
silently exposed to the model until their protocol and consent semantics are
stable.
