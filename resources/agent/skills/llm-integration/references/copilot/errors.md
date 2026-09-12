# Copilot — Errors + retry + token refresh

Error handling for the Copilot API. The error envelope is OpenAI-shaped for
chat/responses and Anthropic-shaped for messages, but auth failures have a
Copilot-specific token-refresh chain.

> Derived from LiteLLM `litellm/llms/github_copilot/{authenticator,common_utils}.py`
> and OpenClaw `extensions/github-copilot/{runtime-auth,runtime-auth-error}.ts`.
> May change without notice. **Derived 2026-09-11.**

## Error envelope

| Transport | Shape | Source |
|---|---|---|
| Chat Completions | OpenAI `{"error": {"message", "type", "code"}}` | litellm inherits `OpenAIConfig.get_error_class` |
| Responses | OpenAI Responses error | litellm inherits `OpenAIResponsesAPIConfig` |
| Anthropic Messages | Anthropic `{"type": "error", "error": {"type", "message"}}` | litellm inherits `AnthropicMessagesConfig` |
| Embeddings | OpenAI-shaped | litellm `embedding/transformation.py` delegates to `OpenAIConfig` |

## Error classes (litellm)

| Class | Trigger | Source |
|---|---|---|
| `GetDeviceCodeError` | Device code request fails or response missing fields | `authenticator.py` |
| `GetAccessTokenError` | Access token poll fails or times out (12 attempts × 5s) | `authenticator.py` |
| `APIKeyExpiredError` | Cached Copilot token past `expires_at` | `authenticator.py` |
| `RefreshAPIKeyError` | Token re-exchange fails after 3 retries | `authenticator.py` |
| `GetAPIKeyError` | No valid token available (refresh failed or missing) | `authenticator.py` |
| `GithubCopilotError` | Base class for all Copilot errors | `common_utils.py` |

## Token refresh chain

```
401 on inference
  │
  ▼
Check cached api-key.json: expires_at > now?
  │ yes → use cached token (race condition)
  │ no  → APIKeyExpiredError
  ▼
_refresh_api_key():
  GET /copilot_internal/v2/token  (Authorization: token <github_token>)
  │ retry up to 3 times
  ▼
Success → cache {token, expires_at, endpoints} → retry inference
Failure → RefreshAPIKeyError → GetAPIKeyError (401 to caller)
```

Source: litellm `authenticator.py` `get_api_key` → `_refresh_api_key`.

openclaw's current path: `resolveCopilotRuntimeAuth` calls
`/copilot_internal/user` with the GitHub token directly. On HTTP error it
throws `CopilotRuntimeAuthError` with reason `http_error` + status. On timeout
(30s) it throws with reason `timeout`.

## Retry policy

| Status | Retry? | Action | Source |
|---|---|---|---|
| 401 | Once | Re-exchange token, retry inference once | litellm `authenticator.py` |
| 429 | Yes (capped) | Exponential backoff + jitter; honor `Retry-After` | cross-provider rule (SKILL.md) |
| 5xx | Yes | Exponential backoff + jitter | cross-provider rule |
| 400/402/404/422 | No | Surface to caller | cross-provider rule |
| Network/timeout | Yes | Exponential backoff | cross-provider rule |

**Token exchange retries:** litellm retries `_refresh_api_key` 3 times.
openclaw uses a 30s timeout on `/copilot_internal/user` with no retry.

## Auth failure modes

| Scenario | Symptom | Recovery | Source |
|---|---|---|---|
| GitHub token expired/revoked | 401 on token exchange | Re-run device flow | litellm |
| Device code expired | `expired_token` error in poll | Re-run device flow | openclaw `login.ts` |
| User denied device login | `access_denied` error in poll | Re-run device flow | openclaw `login.ts` |
| Slow down on poll | `slow_down` error | Increase interval by 5s | openclaw `login.ts` |
| Fine-grained PAT rejected by `/v2/token` | 401 on exchange | Use `/copilot_internal/user` path instead | openclaw `runtime-auth.ts` comment |
| Untrusted `endpoints.api` URL | Parse error | Fall back to default base URL | openclaw `parseCopilotApiBaseUrl` |

## Usage / quota

`GET /copilot_internal/user` also returns quota snapshots (openclaw `usage.ts`):

| Field | Meaning |
|---|---|
| `quota_snapshots.premium_interactions.percent_remaining` | Premium interactions remaining |
| `quota_snapshots.chat.percent_remaining` | Chat interactions remaining |
| `copilot_plan` | Plan name string |

## Edge cases

- **Never log tokens:** The exchange response contains `token` (Copilot API
  key) and the GitHub access token. Log only header names and status codes,
  never values (cross-provider rule).
- **GHE domain mismatch:** A tenant token sent to the public endpoint (or vice
  versa) produces an opaque 401. openclaw warns once on rejected config domains
  (`warnOnceOnRejectedConfigDomain`).
- **Caller-controlled `api_base` on messages:** litellm intentionally ignores
  caller-supplied `api_base` for `/v1/messages` to avoid leaking the Copilot
  bearer token to a caller-controlled URL (`messages/transformation.py`).
- **Token file race:** litellm caches the Copilot token to `api-key.json`.
  Concurrent processes may race on the file; the `expires_at` check is
  best-effort.

## Related files

- [README.md](README.md) — router + auth chain
- [chat-completions.md](chat-completions.md) — chat request/response
- [models.md](models.md) — model catalog
- [stream.md](stream.md) — SSE streaming
- OpenAI errors: [`../openai/errors.md`](../openai/errors.md)
- Anthropic errors: [`../anthropic/errors.md`](../anthropic/errors.md)
