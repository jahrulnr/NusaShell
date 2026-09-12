# OpenCode — OpenAI Chat protocol (`reasoning_content`)

Used for native OpenAI Chat Completions and openai-compatible hosts
(DeepSeek, Together, and similar).

## Endpoint + auth

- Path: `POST {baseURL}/chat/completions` (OpenAI default base
  `https://api.openai.com/v1`)
- Auth: typically `Authorization: Bearer` via the provider configure layer.
  Session affinity headers still come from [headers.md](headers.md).

## Request contract

Assistant message shape (relevant fields):

```json
{
  "role": "assistant",
  "content": "visible answer or null",
  "tool_calls": [],
  "reasoning_content": "thinking text"
}
```

| Field | From canonical parts |
|---|---|
| `content` | Join of `text` parts; **`null`** if none |
| `reasoning_content` | Join of `reasoning` parts; else fallback from `openaiCompatible.reasoning_content` native metadata |
| `tool_calls` | From `tool-call` parts |

The OpenCode client always sends a stream: `stream: true`, with
`stream_options.include_usage: true` for the usage chunk expected by this
client path.

Optional: `reasoning_effort`, `store`, generation knobs (`max_tokens`,
`temperature`, …).

## Response contract (SSE deltas)

```json
{
  "choices": [{
    "delta": {
      "content": "…",
      "reasoning_content": "…",
      "tool_calls": []
    },
    "finish_reason": null
  }],
  "usage": {
    "prompt_tokens": 0,
    "completion_tokens": 0,
    "completion_tokens_details": { "reasoning_tokens": 0 }
  }
}
```

Parser rules:

1. `delta.reasoning_content` → reasoning deltas.
2. First `delta.content` → end reasoning, then text deltas.
3. Tool-call deltas also end reasoning before accumulating tools.
4. `completion_tokens_details.reasoning_tokens` → usage `reasoningTokens`.

## Workflow

1. Prove with an openai-compatible host that returns `reasoning_content`.
2. Confirm replay includes `reasoning_content` on assistant messages when
   interleaved field is set ([provider-transform.md](provider-transform.md)).
3. Do not use this file for Google AI Studio native calls.

## Edge cases

- Reasoning-only turn → `content: null` + `reasoning_content` set (valid).
- Empty `reasoning_content` may still need to be **sent back** (DeepSeek).
- Official OpenAI Chat may not emit `reasoning_content`; the field is for
  compatible hosts / continuation metadata.

## Error handling

Public OpenAI envelope → `../openai/errors.md`. Compatible hosts may differ;
fix the body before retry.
