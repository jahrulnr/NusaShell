# Gemini — Errors, quotas, retries

## Error envelope

Typical REST body:

```json
{
  "error": {
    "code": 429,
    "message": "…",
    "status": "RESOURCE_EXHAUSTED",
    "details": []
  }
}
```

| HTTP | status / signal | Meaning | Action |
|---|---|---|---|
| 400 | `INVALID_ARGUMENT` | Bad schema, thinking combo, missing thought signature, role order | Fix request; never blind-retry |
| 401 | `UNAUTHENTICATED` | Bad key; Bearer on native host; “Expected OAuth 2…” / `ACCESS_TOKEN_TYPE_UNSUPPORTED` | Use `x-goog-api-key` with AI Studio key; Vertex uses OAuth/ADC |
| 403 | `PERMISSION_DENIED` | Key/project not allowed for model | Fix billing/access |
| 404 | `NOT_FOUND` | Retired / region-unavailable model | Pick another id from live `GET /models` |
| 429 | `RESOURCE_EXHAUSTED` | RPM/TPM/RPD / free-tier | Honor `Retry-After` **with cap**; free-tier → enable billing |
| 500/503 | `INTERNAL` / `UNAVAILABLE` | Google-side | Backoff + retry |
| — | `DEADLINE_EXCEEDED` | Upstream timeout | Retry with idle-timeout streaming |

OpenClaw maps: `UNAVAILABLE`→overloaded, `DEADLINE_EXCEEDED`→timeout,
`INTERNAL`→server_error.

## Auth pitfalls

- Native AI Studio host expects **`x-goog-api-key`** (query `?key=` also works
  for some probes). `Authorization: Bearer` is for OAuth/Vertex — using Bearer
  with an API key often fails with misleading OAuth errors.
- Hermes also rejects **free-tier** AI Studio keys at setup (RPD ≤ ~1000 or
  `free_tier` in 429 body) — agent loops burn quota in a few turns.

## Rate limits & Retry-After

Gemini free/daily quotas can return **`Retry-After` measured in hours**.

```text
retryable = (408, 429, 500, 502, 503, 504) or network/timeout
if retryable:
  if 429 and Retry-After present:
    if value <= MAX_RETRY_AFTER: sleep exactly that
    else: DO NOT sleep beyond the wait budget — surface a retryable state
  else: exponential backoff + jitter (3–5 attempts)
else: surface status + message
```

Same cross-provider rule as the parent skill: cap long Retry-After according to
the product wait budget.

## Edge cases

- `finishReason: SAFETY` / blocked candidate with HTTP 200 → treat as
  content failure, not success.
- `MAX_TOKENS` with omitted `maxOutputTokens` looks like a model bug —
  set the ceiling ([generate-content.md](generate-content.md)).
- Insufficient quota vs momentary rate limit: both may be 429 — read message
  / `free_tier` / billing hints before retrying.

## Error handling checklist

1. Log `error.status`, `error.message`, and request model id (never the key).
2. Classify: fix-request vs pace vs billing vs outage.
3. Streamed calls: apply idle timeout; mid-stream error = failed turn.
