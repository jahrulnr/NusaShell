# Anthropic — Messages API

Primary API for all Claude work.

- Endpoint: `POST https://api.anthropic.com/v1/messages`
- Auth: `x-api-key: $ANTHROPIC_API_KEY` (or `Authorization: Bearer` — not both)
- Version header: `anthropic-version: 2023-06-01` (required)
- Token counting: `POST /v1/messages/count_tokens` (same body shape — use it to
  budget before sending, especially with thinking)

## Request contract

```json
{
  "model": "claude-opus-5",
  "max_tokens": 4096,
  "system": "You are a concise assistant.",
  "messages": [
    {"role": "user", "content": "Hello"},
    {"role": "assistant", "content": "Hi! How can I help?"},
    {"role": "user", "content": [{"type": "text", "text": "Explain SSE in one paragraph."}]}
  ],
  "thinking": {"type": "adaptive", "display": "summarized"},
  "tools": [],
  "tool_choice": {"type": "auto"},
  "output_config": {"effort": "high"},
  "cache_control": {"type": "ephemeral"},
  "metadata": {"user_id": "opaque-user-hash"},
  "service_tier": "auto",
  "stop_sequences": [],
  "stream": false
}
```

| Param | Notes |
|---|---|
| `model` | See [model-list.md](model-list.md). Snapshots are pinned; dateless IDs are snapshots from the 4.6 generation on |
| `max_tokens` | **Required.** Max output incl. thinking. `0` = pre-warm the prompt cache without generating (returns empty `content`, `stop_reason: "max_tokens"`). Ceilings: 128k (most current models), 64k (Haiku 4.5 / Sonnet 4.5 / Opus 4.5) |
| `messages` | Alternating `user`/`assistant` (+ mid-conversation `system` turns on some models — see edge cases). Consecutive same-role turns are merged. Max **100,000 messages** per request. Final `assistant` turn = prefill (see edge cases) |
| `system` | Top-level string or text-block array (with `cache_control`) — the default. Mid-conversation `role: "system"` turns are also supported (see edge cases) |
| `thinking` | `adaptive` (new models) / `enabled`+`budget_tokens` (≤4.6 only) / `disabled`. See [thinking.md](thinking.md) |
| `tools` / `tool_choice` | See [tools.md](tools.md) |
| `output_config` | `effort`: `low\|medium\|high\|xhigh\|max`; `format`: `{type: "json_schema", schema}` (structured outputs). Effort defaults to `high` on current models |
| `cache_control` | Top-level = automatic caching (one breakpoint slot). See [prompt-caching.md](prompt-caching.md) |
| `temperature` / `top_p` / `top_k` | **Rejected with 400 on current models** (Fable/Mythos 5.x, Opus 4.7–5, Sonnet 5) whenever non-default. Leave them out |
| `metadata.user_id` | Opaque abuse-detection id (hash/uuid — never PII). Max 512 chars |
| `service_tier` | `auto` (priority capacity when available) vs `standard_only` |
| `container` | Reuse across requests for code-execution; can load Skills `{skill_id, type, version}` |
| `stop_sequences` | Custom stop strings → `stop_reason: "stop_sequence"` + echoed `stop_sequence` |

### Content blocks (request side)

| Block `type` | Key fields | Notes |
|---|---|---|
| `text` | `text` | Empty text blocks can't be cached |
| `image` | `source`: base64 / url / `file_id`; jpeg·png·gif·webp | `transformations.oversized_image: "error"` opts out of silent downscaling |
| `document` | PDF / plain text / url / `file_id`; `citations` config | Enables `citations` on text responses |
| `search_result` | `title`, `source`, `content` | For citing provided search results |
| `thinking` / `redacted_thinking` | `signature` / `data` | Pass back **exactly** as received |
| `tool_use` / `tool_result` | `id`+`name`+`input` / `tool_use_id`+`content` | See [tools.md](tools.md) |
| `container_upload` | `file_id` | File into the code-execution container |

## Response contract

```json
{
  "id": "msg_...",
  "type": "message",
  "role": "assistant",
  "model": "claude-opus-5",
  "content": [
    {"type": "thinking", "thinking": "…", "signature": "…"},
    {"type": "text", "text": "…"}
  ],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "stop_details": null,
  "usage": {
    "input_tokens": 2095,
    "output_tokens": 503,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0,
    "cache_creation": {"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0},
    "output_tokens_details": {"thinking_tokens": 0},
    "service_tier": "standard"
  }
}
```

- Iterate `content[]` by `type` — never assume order or that `content[0]` is text.
  With thinking + tools, a turn can carry `thinking` → `tool_use` → (next call)
  `thinking` → `text`.
- Total input tokens = `input_tokens + cache_creation_input_tokens + cache_read_input_tokens`.

### `stop_reason`

| Value | Meaning | Handle |
|---|---|---|
| `end_turn` | Natural completion | Done |
| `max_tokens` | Hit the cap (incl. thinking) | Raise `max_tokens` or stream; response is truncated |
| `stop_sequence` | Matched a custom stop string | Check `stop_sequence` |
| `tool_use` | Model wants tools | Execute, send `tool_result`, call again |
| `pause_turn` | Long-running server tool paused | Send the response back **as-is** to continue |
| `refusal` | Streaming classifier intervened | Check `stop_details` (`category`, `explanation`); don't retry blindly |
| `model_context_window_exceeded` | Context window exhausted (4.5+) | Trim history (context editing / compaction) |

## Usage fields

- `usage.output_tokens_details.thinking_tokens` — how much billing was internal
  reasoning (output tokens are billed in full even when thinking is hidden).
- `usage.server_tool_use` — per-tool request counts (web search, web fetch) for
  usage-based charges.
- Cache fields — see [prompt-caching.md](prompt-caching.md).

## Workflow

1. Prove connectivity with a minimal curl (one user message, `max_tokens` set)
   before writing code.
2. Build the request: `model` + `system` + `messages`; add `thinking`/effort
   deliberately — not by default ([thinking.md](thinking.md)).
3. Parse `content[]` by block `type`; keep `thinking`/`redacted_thinking` blocks
   untouched for the next turn.
4. Branch on `stop_reason` (table above) — the tool loop lives there.
5. Record returned `usage` when available; watch
   `cache_read_input_tokens` after you add caching.
6. Add `stream: true` for user-facing chat ([stream.md](stream.md)); tools after
   that if needed.

## Edge cases

- **`max_tokens` is mandatory.** Omitting it is a 400. Very large values should
  be streamed (SDKs force streaming above ~21,333 output tokens).
- **Prefill is dead on 4.6+.** A trailing `assistant` message returns 400
  (`assistant message prefill` unsupported) on Claude 4.6 and later. Use
  structured outputs (`output_config.format`) or system instructions instead.
  On ≤4.5 the final assistant turn continues the response.
- **Consecutive turns merge.** Two user messages in a row are combined into one
  turn — don't rely on message boundaries the model never saw.
- **Mid-conversation system messages.** `role: "system"` inside `messages` is
  supported on Fable/Mythos 5.x, Opus 5, and Opus 4.8 — **not** Sonnet 5.
  Placement is strict: it must immediately follow a `user` turn (or tool
  results) and be last or followed by an assistant turn; any other position is
  a 400. Use it for operator instructions added mid-session so the cached
  prefix stays intact ([prompt-caching.md](prompt-caching.md)).
- **Thinking toggling is per-turn, never mid-turn.** Toggling inside a tool loop
  silently disables thinking (no error) — see [thinking.md](thinking.md).
- **Request size:** Messages 32 MB → 413 `request_too_large` beyond that.
- **No multimodal output here.** This API is text + image/PDF **input** only;
  TTS/STT/image generation are not Anthropic surfaces.
- Token counts ≠ visible content: `usage` counts model-format tokens, so an
  empty-looking response can still bill output tokens.

## Error handling

See [errors.md](errors.md). Request-shape 400s almost always name the offending
field (`messages.{i}.content.{j}`) — log it and fix the payload, never blind
retry.
