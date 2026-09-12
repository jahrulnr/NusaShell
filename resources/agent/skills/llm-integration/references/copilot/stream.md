# Copilot — Streaming

SSE streaming for all three Copilot transports: Chat Completions, Responses,
and Anthropic Messages. Each follows its native SSE shape with Copilot-specific
quirks.

> Derived from LiteLLM `litellm/llms/github_copilot/{chat,responses,messages}/`
> and OpenClaw `extensions/github-copilot/stream.ts`. May change without
> notice. **Derived 2026-09-11.**

## Transport SSE shapes

| Transport | Endpoint | SSE shape | Terminator | Source |
|---|---|---|---|---|
| Chat Completions | `/chat/completions` | OpenAI `data: {chunk}\n\n` | `data: [DONE]` | litellm `chat/transformation.py` (inherits `OpenAIConfig`) |
| Responses | `/responses` | OpenAI Responses typed events | `response.completed` | litellm `responses/transformation.py` (inherits `OpenAIResponsesAPIConfig`) |
| Anthropic Messages | `/v1/messages` | Anthropic named events | `message_stop` | litellm `messages/transformation.py` (inherits `AnthropicMessagesConfig`) |

## Request

Add `"stream": true` to the request body. Set `Accept: text/event-stream`.
All editor headers from [README.md](README.md) still apply.

## Chat Completions streaming

Standard OpenAI SSE: `data: {"choices":[{"delta":{"content":"..."}}]}\n\n`,
terminated by `data: [DONE]`. No Copilot-specific deviations observed in
source. Usage may not be present in streaming chunks (openclaw sets
`supportsUsageInStreaming: false` for Gemini models).

## Responses streaming

OpenAI Responses API typed events (`response.output_item.added`,
`response.output_text.delta`, `response.reasoning.delta`,
`response.output_item.done`, `response.completed`).

### Quirk: unstable item ids

Copilot tags each SSE event of a single output item with a **different item
id**, causing clients that key streaming state by item id (e.g. Vercel AI SDK)
to crash with "reasoning part \<id\> not found". LiteLLM rewrites all sub-event
`item_id` fields to one stable id per `output_index`, anchored from
`output_item.added` (`_normalize_stream_item_id` in
`responses/transformation.py`).

### Encrypted reasoning content

Copilot uses `encrypted_content` in reasoning items to maintain conversation
state across turns. The parent OpenAI Responses handler strips this field,
causing "encrypted content could not be verified" errors on multi-turn
requests. LiteLLM preserves `encrypted_content` in `_handle_reasoning_item`
while still filtering `status=None`.

## Anthropic Messages streaming

Standard Anthropic SSE named events (`message_start`, `content_block_start`,
`content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`).
No Copilot-specific deviations in the wire format.

### Replay policy for Claude

openclaw strips `thinking` and `redacted_thinking` blocks from assistant
messages before replaying history to Copilot (`replay-policy.ts`
`stripCopilotAssistantThinkingMessages`). If all content is stripped, it
inserts `[assistant reasoning omitted]` as a text placeholder. This applies
only to Claude models.

### Ephemeral cache control

openclaw applies Anthropic ephemeral `cache_control` markers to the payload
before streaming (`applyAnthropicEphemeralCacheControlMarkers` in `stream.ts`).

## Dynamic streaming headers

Built per-request from message analysis (openclaw `stream.ts`
`buildCopilotDynamicHeaders`):

| Header | Condition |
|---|---|
| `X-Initiator: agent` | Last message is `assistant`, or last `user` message contains `tool_result` |
| `X-Initiator: user` | Last message is `user` without tool_result |
| `Copilot-Vision-Request: true` | Any user/toolResult message contains image content |

## Workflow

1. Build request body with `stream: true`.
2. Set `Accept: text/event-stream` + all editor headers.
3. Open SSE connection; reset idle timeout on every received chunk.
4. For Responses transport: normalize item ids per `output_index`.
5. For Responses reasoning: preserve `encrypted_content` for multi-turn.
6. For Claude: strip thinking blocks from replayed history.
7. Assemble final response from chunks; extract `usage`.

## Edge cases

- **No native WebSocket:** Copilot Responses does not support WebSocket
  (litellm `supports_native_websocket` returns `False`). Use SSE only.
- **Tool ID normalization:** Anthropic tool IDs must match
  `^[a-zA-Z0-9_-]{1,64}$`. openclaw normalizes invalid IDs and deduplicates
  collisions before sending (`normalizeCopilotAnthropicToolIds` in `stream.ts`).
- **Connection-bound ids:** openclaw sanitizes replay response payloads to
  remove connection-bound ids that cannot survive a new request
  (`connection-bound-ids.ts`).

## Related files

- [README.md](README.md) — router + auth chain
- [chat-completions.md](chat-completions.md) — chat request/response
- [models.md](models.md) — model catalog + transport selection
- [errors.md](errors.md) — error handling
- OpenAI streaming: [`../openai/stream.md`](../openai/stream.md)
- Anthropic streaming: [`../anthropic/stream.md`](../anthropic/stream.md)
