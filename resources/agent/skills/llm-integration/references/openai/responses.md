# OpenAI — Responses API

Primary API for all new OpenAI work. Prefer this over Chat Completions unless
maintaining legacy code.

- Endpoint: `POST https://api.openai.com/v1/responses`
- Auth: `Authorization: Bearer $OPENAI_API_KEY`
- Optional headers: `OpenAI-Organization`, `OpenAI-Project`

## Request contract

```json
{
  "model": "gpt-5.6",
  "input": "string OR array of items",
  "instructions": "system-level prompt",
  "tools": [],
  "previous_response_id": null,
  "store": true,
  "max_output_tokens": 4096,
  "reasoning": {"effort": "medium", "summary": "auto"},
  "text": {"format": {"type": "text"}, "verbosity": "medium"},
  "include": [],
  "prompt_cache_key": "session-or-user-id",
  "prompt_cache_options": {"mode": "implicit", "ttl": "30m"},
  "parallel_tool_calls": true,
  "service_tier": "auto",
  "stream": false
}
```

- `input` as string: single-turn shortcut. As array: typed items —
  `{type: "message", role, content}` (content = string or parts:
  `input_text`, `input_image`, `input_file`), `function_call`,
  `function_call_output`, and model-emitted items replayed back.
- `instructions`: system prompt. **Resend every turn** — `previous_response_id`
  does **not** carry over top-level `instructions`; omitting them silently
  drops your system prompt in multi-turn chats.
- `store: true` (default) persists the response server-side, enabling
  `previous_response_id` chaining. `store: false` for zero-data-retention —
  then send full history each turn.
- `reasoning.effort`: `minimal|low|medium|high` — reasoning models only.
  `reasoning.summary`: `auto|concise|detailed` (model support varies — do not
  send for models without reasoning-summary support).
- `text.verbosity`: `low|medium|high` — GPT-5-family output length control;
  unsupported models ignore-or-error it.
- `include`: extra output data — `reasoning.encrypted_content` (stateless
  reasoning replay, see below), `message.output_text.logprobs`,
  `code_interpreter_call.outputs`, `file_search_call.results`,
  `web_search_call.action.sources`, `computer_call_output.output.image_url`.
- `prompt_cache_key`: stable key (session id / user id) that routes requests
  sharing a prefix to the same cache — improves hit rates under load.
  **Required on GPT-5.6+ for reliable cache matching.** Changing the key
  between related requests causes cache misses.
- `prompt_cache_options`: `{mode: "implicit"|"explicit", ttl: "30m"}` +
  `prompt_cache_breakpoint` blocks (up to 4 per request, GPT-5.6+). Explicit
  breakpoints mark reusable boundaries; implicit is the default.
- `parallel_tool_calls: false` disables parallel tool calls (also a
  cache-affecting setting).
- `service_tier`: `auto|default|flex|priority` — `flex` is ~50% cheaper with
  slower/variable latency; `priority` is premium latency.
- `prompt`: reusable server-side prompt template reference (`{id, variables,
  version}`) as an alternative to inline instructions.
- `context_management` (compaction): replaces earlier conversation content
  with a compacted context for very long conversations — note it invalidates
  prompt-cache reuse from the first changed token.

## Response contract

```json
{
  "id": "resp_...",
  "status": "completed",
  "output": [
    {"type": "reasoning", "summary": [...]},
    {"type": "message", "role": "assistant",
     "content": [{"type": "output_text", "text": "..."}]},
    {"type": "function_call", "call_id": "...", "name": "...", "arguments": "..."}
  ],
  "usage": {
    "input_tokens": 0, "output_tokens": 0,
    "output_tokens_details": {"reasoning_tokens": 0}
  },
  "incomplete_details": null
}
```

- Extract text: iterate `output[]`, take `type == "message"` items, concat all
  `content[].text` where `type == "output_text"`. There may be **multiple**
  message items — never read `output[0]` blindly.
- `status`: `completed|failed|in_progress|incomplete`. On `incomplete`, read
  `incomplete_details.reason` (usually `max_output_tokens`).

## Workflow

1. Prove connectivity with a minimal curl (one string input) before writing code.
2. Build the request builder: model + instructions + input items.
3. Parse `output[]` by `type` — dispatch per item type, never assume order.
4. Record returned `usage` when available.
5. Add `stream: true` last (see `stream.md`), tools after that (see `tools.md`).

For a dependency-free Go web-chat and one-shot request example, see
[`scripts/samples/README.md`](../../scripts/samples/README.md).

## Edge cases

- **Reasoning models** (`gpt-5*`, `o*`): reject `temperature`/`top_p` — do not
  send them. `max_output_tokens` includes reasoning tokens; a low cap yields
  empty text with `status: "incomplete"`, not an error.
- **`previous_response_id` history is append-only.** You cannot edit or delete
  a past turn. If the UI allows editing/deleting history, use `store: false`
  and send the full item list each turn.
- **Reasoning items** appear in `output[]` before the message. With
  `store: false`, reasoning items are not replayable **unless** you request
  `include: ["reasoning.encrypted_content"]` — the response then carries an
  encrypted reasoning blob you pass back verbatim on the next request
  (decrypted in-memory only, never persisted). This is the stateless/ZDR
  pattern; without it, drop reasoning items from replayed history.
- **Refusals** arrive as content part `type: "refusal"` — handle explicitly,
  do not treat as empty text.
- Background mode (`background: true`) returns immediately; poll
  `GET /v1/responses/{id}`. Use only for long-running generation.

## Prompt caching (cost control)

- Cache hits show up as `usage.input_tokens_details.cached_tokens` (and
  `cache_write_tokens` on GPT-5.6+) — bill cached input at a large discount.
- **Cache-affecting settings** (any change invalidates reuse from that token
  onward): `model`, `tools` (names/schemas/order), `parallel_tool_calls`,
  `text.format`, `reasoning.effort`, `text.verbosity`, `context_management`.
  Keep them stable across a conversation; put per-request content last.
- `prompt_cache_key` influences routing, not a guarantee — use one stable key
  per conversation (never per-request/timestamp keys). Diagnostics codes
  (`prompt_cache_key_changed`, `reasoning_effort_changed`,
  `verbosity_changed`) explain misses.
- On GPT-6 Astra, change reasoning effort mid-conversation via a
  `configuration_update` input item instead of the request-level param —
  preserves the cached prefix.

## Error handling

See `errors.md` for codes and retry policy. Request-shape errors (400) almost
always name the offending `param` — surface it in logs.
