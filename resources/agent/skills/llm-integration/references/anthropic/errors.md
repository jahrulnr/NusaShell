# Anthropic — Errors

## HTTP status map

| Status | `error.type` | Meaning / handling |
|---|---|---|
| 400 | `invalid_request_error` | Malformed/unsupported request (also used for other 4xx). Fix the payload — the message usually names the field |
| 401 | `authentication_error` | Bad/expired/revoked key, or wrong scheme for the credential type |
| 402 | `billing_error` | Billing/payment problem |
| 403 | `permission_error` | Key lacks access to the resource |
| 404 | `not_found_error` | Wrong path / unknown id |
| 409 | `conflict_error` | Concurrent modification / duplicate — resolve, then retry |
| 413 | `request_too_large` | Over the endpoint size limit (Messages 32 MB) |
| 429 | `rate_limit_error` | Rate limit or spend cap. Spend-cap 429s have **no** `retry-after` and keep failing until access resumes |
| 500 | `api_error` | Internal error — retry with backoff |
| 504 | `timeout_error` | Processing timed out — switch to streaming |
| 529 | `overloaded_error` | API overloaded — retry with backoff |

## Error envelope

```json
{
  "type": "error",
  "error": {"type": "not_found_error", "message": "…"},
  "request_id": "req_…"
}
```

- Every response (success or error) carries a `request-id` header; error bodies
  repeat it as `request_id`. Log it — it's what support needs.
- `type` values and messages may expand over time; branch on status + `type`,
  never on message text.

## Retry policy (aligns with the skill's cross-provider rules)

- Retry: 409 (after resolving), 429 (honor `retry-after`, cap it), 500, 504
  (prefer streaming), 529, network/timeouts — exponential backoff + jitter.
- Never blind-retry 400/401/402/403/404/413.
- SDKs auto-retry transient failures twice by default and honor `retry-after`.

## Streaming caveat

A stream can fail **after** HTTP 200 — the failure arrives as an `error` SSE
event (`{"type":"error","error":{...}}`), not an HTTP error. Handle both layers;
recovery guidance in [stream.md](stream.md).

## Long requests

- Anything above ~10 minutes: stream and/or use the Message Batches API.
- Idle networks drop connections; enable TCP keep-alive / reset an idle timeout
  on every chunk. SDKs require streaming above ~21,333 `max_tokens`.

## Common validation 400s (and the fix)

| Message clue | Fix |
|---|---|
| `assistant message prefill` unsupported | Model ≥4.6 rejects trailing assistant turns — use structured outputs / system instructions |
| `thinking … blocks … cannot be modified` | Replay thinking + redacted_thinking exactly as received, in order |
| `thinking.type.enabled is not supported` | 4.7+ removed manual budgets — use `adaptive` + effort |
| `adaptive thinking is not supported` | ≤4.5 model — use `enabled` + `budget_tokens` |
| `thinking.type.disabled is not supported` | Fable/Mythos 5.x always think — omit the param |
| `tool_choice … not supported` | Fable 5.1 / Mythos 5.1: use `auto` (+ `strict` tools) |
| `block_binding` extra input | Needs beta header `thinking-binding-controls-2026-08-01` |
| `temperature` / `top_p` / `top_k` rejected | Current models accept default values only — drop the param |

## Platform note

Claude Platform on AWS adds `x-amzn-requestid` alongside `request-id` (use the
AWS id for CloudTrail, the Anthropic id for support). If every request fails
with "Outbound web identity federation is disabled", enable it once per AWS
account.

Related: [messages.md](messages.md) · [stream.md](stream.md) · [thinking.md](thinking.md)
