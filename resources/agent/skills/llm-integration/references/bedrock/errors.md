# Bedrock — Errors & retry policy

AWS error semantics for the Bedrock runtime. Bedrock does not use OpenAI-style
`{"error": {"message": ...}}` envelopes; errors are AWS service exceptions
with a `__type` class name + `message` field, delivered as JSON for non-stream
calls or as **error frames** inside the event-stream for ConverseStream.
Verified 2026-09-11.

## Endpoint + auth

Errors are returned by the same endpoints in [converse.md](converse.md) /
[stream.md](stream.md). Auth failures surface as `AccessDeniedException` /
`ExpiredTokenException` from the SigV4 layer or a `401` for an invalid bearer
token.

## Request contract (error envelope)

Non-streaming HTTP error response:
```json
{
  "__type": "ValidationException",
  "message": "The provided model identifier is invalid"
}
```
The HTTP status code and `__type` together identify the exception. AWS SDKs
parse this into a typed exception (`botocore.exceptions.ClientError` with
`e.response['Error']['Code']`).

Streaming error frame (inside the binary event-stream, `:message-type: error`):
```json
{"__type": "modelStreamErrorException", "message": "A streaming error occurred"}
```
These can arrive **after** content deltas have already been emitted.

## Response contract (exception table)

| Exception | HTTP | Retryable | Cause / action |
|---|---|---|---|
| `ValidationException` | 400 | **No** | Bad request body, invalid model id, unsupported param, conflicting `toolChoice`, tampered reasoning `signature`. Fix the request. |
| `AccessDeniedException` | 403 | **No** | Missing IAM permission (`bedrock:InvokeModel`), model access not enabled in console, wrong region. Grant perms / enable access. |
| `ResourceNotFoundException` | 404 | **No** | Model id or inference profile does not exist in this region. Check id + region. |
| `ThrottlingException` | 429 | **Yes** (backoff) | Exceeded account quotas (TPM/RPM). Honor any `Retry-After` within the configured wait budget; use cross-region inference profiles or request quota increase. |
| `ModelTimeoutException` | 408 | **Yes** (backoff) | Request exceeded the model timeout. Retry with backoff; consider smaller input. |
| `ModelNotReadyException` | 424 / 500 | **Yes** (backoff) | Imported/custom model evicted from the on-demand fleet; restoration in progress. AWS SDK auto-retries by default; configure max retries. |
| `modelStreamErrorException` | 424 | **Yes** (re-send whole request) | Streaming-specific failure mid-stream. Bedrock streams are not resumable — re-issue the full request. |
| `InternalServerException` | 500 | **Yes** (backoff) | Server error. Retry with exponential backoff + jitter. |
| `ServiceUnavailableException` | 503 | **Yes** (backoff) | Temporary capacity constraint (not quota). Retry; consider a different region or cross-region inference. |
| `ExpiredTokenException` | 403 | **No** (refresh creds) | STS session token expired. Refresh credentials and retry. |
| `TooManyTagsException` | 400 | **No** | Too many `requestMetadata` tags. Reduce count. |
| `ServiceQuotaExceededException` | 402/429 | **No** (quota) | Provisioned throughput limit hit. Request quota increase. |

## Workflow

1. Parse `__type` (or HTTP status if `__type` absent) from the error body.
2. Classify: retryable (429/408/424/500/503 + `ModelNotReadyException`) vs
   non-retryable (400/401/403/404).
3. For retryable: exponential backoff + jitter. Honor `Retry-After` if
   present, but cap it at the configured product wait budget — Bedrock
   throttling can report long waits.
   Sync retries with the 60-second per-minute quota refresh cycle for TPM
   limits.
4. For `ModelNotReadyException`: longer initial backoff (restoration can take
   seconds to minutes depending on model size). AWS SDKs auto-retry this.
5. For streaming errors: re-send the **entire** ConverseStream request. There
   is no resume cursor. Discard any partial state from the failed stream.
6. For `AccessDeniedException`: do not retry — check IAM perms + console model
   access + region. Surface to the caller.
7. For `ExpiredTokenException`: refresh the credential chain (STS /
   `aws sso login` / instance metadata) and retry once.

## Edge cases

- **Misleading messages.** An unsupported `serviceTier` can return
  `ValidationException` with "The provided model identifier is invalid" — the
  model id is fine; the tier is the problem. Check tier support before
  assuming the id is wrong.
- **429 vs 503 distinction.** `ThrottlingException` (429) = your account
  quotas; `ServiceUnavailableException` (503) = shared capacity strain. Both
  retry, but 503 may need a region switch if persistent. CloudTrail can show
  both on the same workload — monitor them separately.
- **Mid-stream errors discard partial output only if your UX requires it.**
  A `modelStreamErrorException` after text deltas means the model started
  answering then failed. You can keep the partial text or drop it; either is
  valid depending on the product. For refusal-safe Claude models, buffer until
  `messageStop` so a mid-stream refusal never exposes partial text.
- **Bearer token expiry.** `AWS_BEARER_TOKEN_BEDROCK` can hold a short-term key
  (up to 12h or session end, whichever is shorter) or a long-term key
  (configured expiry, IAM-user-backed). Don't assume 12h for all keys. A
  `401`/`ExpiredTokenException` with a bearer token means regenerate it — for
  short-term keys via a token generator (e.g. `aws-bedrock-token-generator`)
  from AWS creds.
- **SigV4 clock skew.** SigV4 rejects requests if the client clock is >5 min
  off. `RequestTimeTooSkewed` -> sync system time, not a retry.
- **Quota vs capacity.** Per-minute TPM/RPM quotas refresh on a 60s cycle.
  Distribute requests across the minute; a burst that fits "per day" can still
  trip the per-minute limit.
- **No `Retry-After` on some 429s.** Bedrock throttling does not always
  include the header — fall back to exponential backoff when absent.

## Error handling (policy summary)

Retryable: `ThrottlingException`, `ModelTimeoutException`,
`ModelNotReadyException`, `modelStreamErrorException`,
`InternalServerException`, `ServiceUnavailableException`, network/timeout.
Non-retryable as-is: `ValidationException`, `AccessDeniedException`,
`ResourceNotFoundException`, `ExpiredTokenException` (refresh then retry
once), `TooManyTagsException`, `ServiceQuotaExceededException`.

Use the AWS SDK built-in retry where possible (it handles `ModelNotReady`
auto-retry + credential refresh). For hand-rolled HTTP, implement exponential
backoff with jitter, base ~1s, a configured cap, max ~5 retries. Never retry a
streaming request incrementally — re-send the whole request.
