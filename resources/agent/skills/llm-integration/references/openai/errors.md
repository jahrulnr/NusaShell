# OpenAI — Errors, retries, rate limits

## Error envelope

```json
{"error": {"message": "...", "type": "invalid_request_error", "param": "temperature", "code": null}}
```

| HTTP | type / code | Meaning | Action |
|---|---|---|---|
| 400 | `invalid_request_error` | Bad param, unsupported param, `context_length_exceeded` | Fix request; never blind-retry |
| 401 | `authentication_error` | Bad/revoked key | Fix credentials |
| 403 | `permission_error` | Org/project not allowed for model | Fix access |
| 404 | `model_not_found` / code `model_not_found` | Typo or retired model | Fix model id |
| 422 | `invalid_request_error` | Schema-level validation failure | Fix body |
| 429 | `rate_limit_exceeded` | TPM/RPM limit | Honor `Retry-After`, backoff |
| 429 | code `insufficient_quota` | Billing exhausted | Fix billing — do not retry |
| 500/503 | `server_error` / overloaded | OpenAI-side | Backoff + retry |

## Rate limits: usage tiers, TPM/RPM per model

OpenAI rate limits are **tier-based and per-model** — unlike OpenRouter's
dynamic per-key limits. Limits apply at **org and project level** (not per
end-user) and use multiple metrics: RPM, RPD, TPM, TPD, IPM (images/min),
audio-minutes — whichever is hit first stops you.

**Usage tiers** (auto-graduation as spend increases):

| Tier | Qualification | Approved monthly usage |
|---|---|---|
| Free | allowed geography | $100 |
| Tier 1 | $5 paid | $100 |
| Tier 2 | $50 paid | $500 |
| Tier 3 | $100 paid | $1,000 |
| Tier 4 | $250 paid | $5,000 |
| Tier 5 | $1,000 paid | $200,000 |

- TPM/RPM tables are **per model per tier** — check the rate-limits docs page
  or the developer console for your org's actual numbers; they change with
  model and tier.
- Long-context models have a **separate rate limit** for long-context
  requests.
- **Programmatic check** (admin key):
  `GET /v1/organization/projects/{project_id}/rate_limits` → per-model
  `max_requests_per_1_minute`, `max_tokens_per_1_minute`,
  `max_images_per_1_minute`, `batch_1_day_max_input_tokens`.
- **Response headers** (read them to pace clients):
  `x-ratelimit-limit-requests`, `x-ratelimit-limit-tokens`,
  `x-ratelimit-remaining-requests`, `x-ratelimit-remaining-tokens`,
  `x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`, plus
  project-scoped variants (`x-ratelimit-remaining-project-tokens`, ...), and
  `Retry-After` on 429.

## Retry policy (language-agnostic)

```text
retryable = (http 408, 429, 500, 502, 503, 504) or network/timeout error
MAX_RETRY_AFTER = configured product wait budget
if retryable:
    if 429 and Retry-After header and value <= MAX_RETRY_AFTER: wait exactly that long
    else: delay = min(base * 2^attempt + jitter, cap)   # base ~1s, 3-5 attempts max
    # Retry-After larger than the cap (Gemini daily quotas can report hours):
    # do NOT sleep — surface the error to the caller instead of blocking.
else: surface the error with type, code, param
```

- LLM calls are safe to retry **only if the first attempt is discarded** —
  never double-apply side effects around a retried call.
- Streaming: a 200 OK does not guarantee success — errors can arrive as
  mid-stream events (see `stream.md`).

## Edge cases

- `context_length_exceeded` (400): trim history — never retry unchanged. See
  `_shared/usecase-patterns.md` for trimming rules.
- `insufficient_quota` vs `rate_limit_exceeded`: both arrive as 429 — check
  the `code`/message; one needs billing, the other pacing.
- **`organization_usage_limit_exceeded`** (429): the org hit its approved
  **monthly usage limit** — not a pacing problem; request a higher limit or
  wait for the month to roll over. Retrying never helps.
- Reasoning models can think for tens of minutes (max effort + slow TPS):
  fixed client timeouts (30–60s) kill valid, already-billed generations —
  you pay for tokens that never arrive. For interactive streams, avoid a short
  fixed total timeout; use an idle timeout that resets on every chunk and add a
  configurable product-level deadline when needed (see `stream.md`).
  Distinguish timeout (unknown outcome, do not blindly retry a side-effectful
  follow-up) from explicit cancellation.
- Rate limits are per-org/project and per-model tier; high volume → Batch API
  (50% cost, 24h window) instead of hammering the sync endpoint.
- 5xx after partial payment of tokens is possible on long generations —
  budget for occasional total loss on very large requests.
