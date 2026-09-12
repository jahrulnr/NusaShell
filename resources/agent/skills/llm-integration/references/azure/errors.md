# Azure OpenAI - Errors, content filtering, retries, rate limits

Azure uses the OpenAI-shaped error envelope plus an Azure-specific
`innererror` for content-filter violations and `prompt_filter_results`/
`content_filter_results` annotations on successful responses.

Verified 2026-09-11 (Azure OpenAI reference; content-filter docs; LiteLLM
`llms/azure/common_utils.py` `AzureOpenAIError` + `process_azure_headers`).

## Error envelope

```json
{"error": {"message": "...", "type": "invalid_request_error", "param": "prompt", "code": "content_filter", "status": 400, "innererror": {...}}}
```

| HTTP | type / code | Meaning | Action |
|---|---|---|---|
| 400 | `invalid_request_error` | Bad/unsupported param, `context_length_exceeded`, bad tool schema, `status` field on item | Fix request; never blind-retry |
| 400 | code `content_filter` | Prompt or output triggered content policy | Modify prompt or filter config; never retry unchanged |
| 401 | `authentication_error` | Bad/revoked key, expired Entra token | Fix credentials / refresh token |
| 403 | `permission_error` / `AuthorizationFailed` | Key/identity lacks RBAC on resource/deployment | Fix RBAC role assignment |
| 404 | `deployment_not_found` / `model_not_found` | Wrong deployment name or resource | Fix deployment/resource name |
| 409 | `conflict` | Deployment already exists / provisioning conflict | Inspect state; do not retry blindly |
| 429 | `rate_limit_exceeded` / `RequestsPerMinuteLimitExceeded` / `TokensPerMinuteLimitExceeded` | TPM/RPM quota (standard) or throughput (PTU) | Honor `Retry-After`, backoff |
| 500/502/503 | `server_error` / `ServiceUnavailable` | Azure-side | Backoff + retry |

## Content filtering (Azure-specific, always on by default)

Prompt blocked -> **400** with `code: content_filter`:

```json
{
  "error": {
    "code": "content_filter",
    "status": 400,
    "innererror": {
      "code": "ResponsibleAIPolicyViolation",
      "content_filter_results": {
        "hate": {"filtered": true, "severity": "medium"},
        "self_harm": {"filtered": false, "severity": "safe"},
        "sexual": {"filtered": false, "severity": "safe"},
        "violence": {"filtered": false, "severity": "safe"},
        "custom_blocklists": [{"filtered": true, "id": "raiBlocklistName"}],
        "profanity": {"filtered": false, "detected": false}
      }
    }
  }
}
```

- Categories: `hate`, `self_harm`, `sexual`, `violence` (severity
  `safe`/`low`/`medium`/`high`), `profanity` (detected bool), `custom_blocklists`.
- `innererror.code` = `ResponsibleAIPolicyViolation`.
- Output blocked mid-generation -> **200** with `finish_reason: "content_filter"`
  and per-choice `content_filter_results` (partial text). Not a hard error.
- Successful 200 responses also carry `prompt_filter_results` (per prompt index)
  and per-choice `content_filter_results` annotations - parse defensively; they
  are absent on public OpenAI.
- Filter config is per-deployment (portal/ARM); you cannot disable via request.
  Some subscriptions can apply for filter modifications.

## Rate limits + response headers

Azure returns OpenAI-style rate-limit headers (LiteLLM `process_azure_headers`
forwards these): `x-ratelimit-limit-requests`, `x-ratelimit-remaining-requests`,
`x-ratelimit-limit-tokens`, `x-ratelimit-remaining-tokens`, plus `Retry-After`
on 429 and `apim-request-id` for correlation.

- **Standard deployments**: per-deployment TPM/RPM (tokens/requests per minute).
  Limits scale with deployment capacity and region.
- **PTU deployments**: throughput-unit limits, not token quota; 429 = throughput
  exhausted. Scale PTU or overflow to a standard deployment.
- Honor `Retry-After` but **cap it** at the configured product wait budget; if
  larger, surface a retryable state instead of blocking (cross-provider rule
  #4, `../../SKILL.md`).

## Retry policy (language-agnostic)

```text
retryable = (http 408, 429, 500, 502, 503, 504) or network/timeout error
MAX_RETRY_AFTER = configured product wait budget
if retryable:
    if 429 and Retry-After header and value <= MAX_RETRY_AFTER: wait exactly that long
    else: delay = min(base * 2^attempt + jitter, cap)   # base ~1s, 3-5 attempts max
else: surface the error with type, code, param, innererror
```

- Never retry 400/401/403/404/409 as-is - they are not transient.
- `content_filter` (400) is never retryable unchanged - modify the prompt.
- LLM calls are safe to retry only if the first attempt is discarded (no
  double-applied side effects).
- Streaming: a 200 OK does not guarantee success - errors can arrive as
  mid-stream events (`stream.md`).

## Edge cases

- `context_length_exceeded` (400): trim history - never retry unchanged.
- Entra ID tokens expire - a 401 mid-session often means token refresh needed,
  not a bad credential. Refresh via the token provider before retrying.
- 429 on PTU vs standard need different fixes (scale PTU vs pace/reduce tokens).
- 5xx after partial token spend is possible on long generations - budget for
  occasional total loss on very large requests.
- `apim-request-id` (not `x-request-id`) is the Azure support correlation id -
  log it with every response/error.
