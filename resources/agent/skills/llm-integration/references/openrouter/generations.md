# OpenRouter — Generations (per-request debug)

Inspect one generation's metadata and stored content — cost, latency, tokens,
provider routing, or the actual prompt/completion. Use when debugging a failed
or unexpected request.

Auth: any valid `OPENROUTER_API_KEY` (regular or management).
Keys: [openrouter.ai/settings/keys](https://openrouter.ai/settings/keys).

Generation IDs look like `gen-1234567890`, `gen-aBcDeFgHiJkLmNoPqRsT`, or
`gen-tts-…` (from TTS `X-Generation-Id`).

Upstream skill: [openrouter-generations](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-generations).
API ref: [get-generation](https://openrouter.ai/docs/api/api-reference/generations/get-generation).

## Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/generation` | GET | Metadata and usage (tokens, cost, latency, model, provider) |
| `/api/v1/generation/content` | GET | Stored prompt and completion text |

Both take query param `id` (the generation ID).

## Positive cases

### Metadata

```bash
curl -G https://openrouter.ai/api/v1/generation \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -d id=gen-1234567890
```

Returns everything **except** prompt/completion text: model & routing, tokens,
cost, performance, status, correlation ids, `provider_responses` fallback chain.

### Content

```bash
curl -G https://openrouter.ai/api/v1/generation/content \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -d id=gen-1234567890
```

Returns stored input (`prompt` and/or `messages`) and output (`completion`,
optional `reasoning`). **Unavailable under Zero Data Retention (ZDR)** —
endpoint returns empty/null content; metadata may still exist.

## Response schemas

### Metadata (`GET /api/v1/generation`)

```json
{
  "data": {
    "id": "gen-3bhGkxlo4XFrqiabUM7NDtwDzWwG",
    "api_type": "completions",
    "model": "openai/gpt-4o",
    "provider_name": "OpenAI",
    "created_at": "2024-07-15T23:33:19.433273+00:00",
    "tokens_prompt": 10,
    "tokens_completion": 25,
    "native_tokens_reasoning": 5,
    "native_tokens_cached": 3,
    "total_cost": 0.0015,
    "usage": 0.0015,
    "upstream_inference_cost": 0.0012,
    "latency": 1250,
    "generation_time": 1200,
    "finish_reason": "stop",
    "streamed": true,
    "is_byok": false,
    "cancelled": false,
    "router": "openrouter/auto",
    "service_tier": "priority",
    "provider_responses": [
      {
        "provider_name": "OpenAI",
        "model_permaslug": "openai/gpt-4o",
        "status": 200,
        "latency": 1200,
        "is_byok": false
      }
    ]
  }
}
```

### Content (`GET /api/v1/generation/content`)

```json
{
  "data": {
    "input": {
      "prompt": "What is the meaning of life?",
      "messages": [
        { "content": "What is the meaning of life?", "role": "user" }
      ]
    },
    "output": {
      "completion": "The meaning of life is a philosophical question...",
      "reasoning": null
    }
  }
}
```

## Key fields reference

### Metadata fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Generation ID (`gen-…`) |
| `model` | string | Model permaslug (e.g. `openai/gpt-4o`) |
| `provider_name` | string\|null | Provider that served the request |
| `api_type` | string | `completions` \| `embeddings` \| `rerank` \| `tts` \| `stt` \| `video` |
| `tokens_prompt` | int\|null | Prompt token count |
| `tokens_completion` | int\|null | Completion token count |
| `native_tokens_reasoning` | int\|null | Reasoning/thinking tokens |
| `native_tokens_cached` | int\|null | Cached input tokens |
| `total_cost` | number | Total cost in USD (what you were charged) |
| `usage` | number | Usage amount in USD |
| `upstream_inference_cost` | number\|null | Provider's cost to OpenRouter |
| `cache_discount` | number\|null | Discount from caching |
| `latency` | number\|null | Total latency in ms |
| `generation_time` | number\|null | Model generation time in ms |
| `moderation_latency` | number\|null | Moderation check time in ms |
| `finish_reason` | string\|null | Why generation stopped (`stop`, `length`, `content_filter`, …) |
| `native_finish_reason` | string\|null | Raw finish reason from provider |
| `streamed` | bool\|null | Whether response was streamed |
| `is_byok` | bool | Whether user's own provider key was used |
| `cancelled` | bool\|null | Whether request was cancelled by client |
| `app_id` | int\|null | OAuth app ID |
| `external_user` | string\|null | External user id (`X-External-User`) |
| `session_id` | string\|null | Session grouping ID |
| `request_id` | string\|null | Request grouping (all gens from one API call) |
| `router` | string\|null | Router used (e.g. `openrouter/auto`) |
| `service_tier` | string\|null | Provider service tier |
| `web_search_engine` | string\|null | Search engine (`exa`, `firecrawl`, …) |
| `num_search_results` | int\|null | Number of search results included |
| `provider_responses` | array\|null | Provider attempt chain (name, status, latency, BYOK) |
| `created_at` | string | Timestamp |

### Content fields

| Field | Type | Description |
|---|---|---|
| `data.input.prompt` | string\|null | Raw prompt text |
| `data.input.messages` | array\|null | Messages `[{role, content}]` |
| `data.output.completion` | string\|null | Model completion text |
| `data.output.reasoning` | string\|null | Chain-of-thought reasoning |

## Common use cases

### Debug a failed generation

1. Pull metadata; inspect `finish_reason`, `cancelled`, `provider_responses`.
2. `finish_reason=length` → hit max tokens.
3. `finish_reason=content_filter` → filtered.
4. `cancelled=true` → client aborted.
5. Multiple `provider_responses` → fallbacks fired (check per-provider status/latency).

### Check cost of one request

Compare `total_cost` (charged to you) vs `upstream_inference_cost` (provider →
OpenRouter). Also note `cache_discount`.

### Review what was sent/received

Pull content (if not ZDR) to verify actual prompt/messages and completion.

### Trace a multi-generation session

Use `request_id` / `session_id` from metadata, then query related rows via
[analytics.md](analytics.md) (e.g. `dimensions: ["generation_id"]`) and drill
back here per id.

## Edge cases

- **ZDR:** content endpoint empty/null is expected — not a 404.
- **403:** generation belongs to another account — do not retry as success.
- TTS / STT / video use the same metadata API; `api_type` differs; TTS often
  exposes id via `X-Generation-Id`.
- 429 / 5xx → retry with backoff; see [errors.md](errors.md).

## Error handling

| HTTP | Meaning |
|---|---|
| 401 | Invalid or missing API key |
| 403 | Not your generation |
| 404 | Unknown id |
| 429 | Rate limited — wait and retry |
| 500 | Server error — retry |
| 502 | Upstream failure — retry |

## Related

- [analytics.md](analytics.md) — fleet usage / spend (management key)
- [errors.md](errors.md) — shared retry / credit errors
- [README.md](README.md) — OpenRouter router
- Docs: [get-generation](https://openrouter.ai/docs/api/api-reference/generations/get-generation)
