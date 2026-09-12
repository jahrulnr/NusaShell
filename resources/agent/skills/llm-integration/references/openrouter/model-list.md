# OpenRouter — Models (discovery / pricing / endpoints)

Discover, search, and compare models: pricing, context, modalities,
`supported_parameters`, and per-provider latency / uptime / throughput.

Upstream: [openrouter-models](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-models).
Docs: [Models overview](https://openrouter.ai/docs/guides/overview/models),
[OpenAPI](https://openrouter.ai/openapi.json).

`OPENROUTER_API_KEY` optional for `GET /models`; often required for richer
endpoint/performance paths — get a key at [openrouter.ai/keys](https://openrouter.ai/keys).

## Decision tree

| Need | HTTP |
|---|---|
| List / filter catalog | `GET /api/v1/models` (+ query params) |
| Informal name → id | Client-side fuzzy match on catalog `id`/`name` |
| Image/audio/file capable | Filter `architecture.input_modalities` |
| Compare N models | Fetch catalog; exact-id match; table pricing |
| Provider latency/uptime | `GET /api/v1/models/{author}/{slug}/endpoints` |

## Positive cases

### List models

```bash
curl -sS 'https://openrouter.ai/api/v1/models' \
  -H "Authorization: Bearer $OPENROUTER_API_KEY"
# → { "data": [ Model, ... ] }
```

Optional query params:

| Param | Example | Effect |
|---|---|---|
| `category` | `?category=programming` | Server-side category filter |
| `supported_parameters` | `?supported_parameters=tools` | Only models advertising that param |

Categories include: `programming`, `roleplay`, `marketing`, `marketing/seo`,
`technology`, `science`, `translation`, `legal`, `finance`, `health`,
`trivia`, `academia`.

### Per-provider endpoints

```bash
curl -sS 'https://openrouter.ai/api/v1/models/anthropic/claude-sonnet-4/endpoints' \
  -H "Authorization: Bearer $OPENROUTER_API_KEY"
# → { "data": { "id", "name", "endpoints": [ Endpoint, ... ] } }
```

Each endpoint typically includes provider tag, status, uptime, latency /
throughput percentiles (last 30m), context / max completion, pricing,
`supported_parameters`, caching flags.

## Contracts

### Model (high-signal)

| Field | Meaning |
|---|---|
| `id` | Permaslug `author/slug` (use in chat / filters) |
| `name` | Display name |
| `pricing.prompt` / `pricing.completion` | USD **per token** (string); ×1e6 → per-million |
| `pricing` cache fields | When present — can cut input cost dramatically |
| `context_length` | Max total tokens |
| `top_provider.max_completion_tokens` | Max output (best provider) |
| `top_provider.is_moderated` | Moderation applied |
| `per_request_limits` | Per-request caps when non-null |
| `supported_parameters` | e.g. `tools`, `structured_outputs`, `reasoning`, `web_search_options` |
| `architecture.input_modalities` / `output_modalities` | `text`, `image`, `audio`, `file`, … |
| `created` | Unix ts — sort by recency |
| `knowledge_cutoff` / `expiration_date` | Dates or null; non-null expiration ⇒ deprecating |
| `links.details` | Link toward endpoints API for this model |
| `canonical_slug`, `hugging_face_id`, `default_parameters` | Present on raw API (scripts may drop them) |

### Endpoint (high-signal)

| Field | Meaning |
|---|---|
| Provider name / tag | Upstream serving this model |
| `status` | API: `0` = operational, non-zero = degraded (UIs often map to labels) |
| `uptime` / last-30m | Availability % |
| `latency_last_30m` | `{p50,p75,p90,p99}` ms |
| `throughput_last_30m` | `{p50,…}` tokens/sec |
| Provider pricing | May differ from catalog top price |
| `supported_parameters` | **Varies by provider** — do not assume catalog union |

### Variant suffixes (routing)

Model ids may use suffixes such as `:free`, `:nitro`, `:floor`, `:thinking`,
`:online` — see [chat-completion.md](chat-completion.md) / provider prefs.
Confirm live catalog; treat as OpenRouter routing helpers, not separate vendors.

## Client-side workflows (no SDK)

### Resolve informal names

1. `GET /models` → match fuzzy against `id` + `name`.
2. Confidence heuristic (same idea as upstream skill): high ≥0.85 use directly;
   medium ≥0.55 confirm; low ≥0.30 ask user.
3. Feed resolved `id` into compare / endpoints / chat.

### Sort / filter locally

After fetch: sort by `created` (newest), `pricing.prompt` (cheapest),
`context_length`, or endpoints throughput/latency. Filter modalities with
`architecture.*_modalities`. Warn on non-null `expiration_date`.

### Compare

Exact id match only (`openai/gpt-4o` ≠ `openai/gpt-4o-mini`). Present
per-million pricing, context, modalities, notable `supported_parameters`,
cache pricing when present.

### Provider pick

From endpoints: highlight lowest p50 latency, highest uptime, cheapest
provider-specific price, and whether required params (e.g. `tools`) are
supported on that provider.

## Edge cases

- OpenAI public `/v1/models` is **not** compatible — OpenRouter catalog is rich;
  OpenAI’s is minimal (`id` only). See `../_shared/provider-matrix.md`.
- Endpoint `supported_parameters` can be a subset of the model’s catalog list.
- Pricing strings need parsing; never assume number type.
- Some scripts reformat endpoints (`status: "operational"`) — raw API may use
  numeric status; handle both if integrating UIs.

## Errors

| HTTP | Meaning |
|---|---|
| 401 | Bad key (when required) |
| 404 | Unknown `author/slug` on endpoints |
| 429 / 5xx | Backoff — [errors.md](errors.md) |

## Related

- [chat-completion.md](chat-completion.md) — use resolved model ids
- [benchmarks.md](benchmarks.md) — quality rankings
- [analytics.md](analytics.md) — spend by `model`
- [images.md](images.md) / [stt.md](stt.md) / [tts.md](tts.md) / [video.md](video.md)
- [README.md](README.md)
