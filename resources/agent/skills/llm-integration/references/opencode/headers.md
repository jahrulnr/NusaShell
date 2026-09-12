# OpenCode — Outbound headers

## Endpoint + auth

Session prepare builds HTTP headers **before** the chosen runtime (AI SDK or
native) sends the call. Provider auth headers are applied by the SDK or the
native auth layer — not as hardcoded secrets in this guide.

## Request contract (header layers)

Merge order (later overlays earlier where keys collide; auth usually comes
from the provider layer):

```text
1. Base session headers (by providerID family)
2. x-parent-session-id          # if parent session
3. model.headers                # per-model catalog / config
4. plugin chat.headers          # optional plugin overlay
```

### Non-`opencode*` providers (includes `google`)

| Header | Value |
|---|---|
| `User-Agent` | OpenCode user-agent |
| `x-session-affinity` | session id |
| `X-Session-Id` | session id |
| `x-parent-session-id` | optional parent id |

### `opencode*` providers

| Header | Value |
|---|---|
| `User-Agent` | same |
| `x-opencode-session` | session id |
| `x-opencode-request` | user message id |
| `x-opencode-client` | client flag |
| `x-opencode-project` | project id when present |

### Google auth (native Google / Gemini path)

| Header | Source |
|---|---|
| `x-goog-api-key` | API key from options or `GOOGLE_GENERATIVE_AI_API_KEY` / catalog envs |
| `Content-Type` | `application/json` on JSON POST |

Do **not** send `Authorization: Bearer <AI Studio key>` on
`generativelanguage.googleapis.com` for the native protocol.

AI SDK Google applies the same API-key header convention internally; the
session still attaches affinity / User-Agent layers above.

## Response contract

Useful response headers for retries: `retry-after`, `retry-after-ms`,
`x-request-id` / `x-goog-request-id`. Those are inbound — not part of outbound
prepare.

## Workflow

1. When debugging “who sent what”, separate **session affinity** headers from
   **auth** headers.
2. For Google 401 with a Bearer-style setup → switch to `x-goog-api-key`.
3. Never log raw API key values.

## Edge cases

- Plugin `chat.headers` may inject arbitrary keys — treat as an overlay; do
  not put secrets in catalog headers that get logged.
- Error reporters should redact sensitive header names.

## Error handling

Missing Google key → auth failure before stream. Wrong auth scheme → 401 from
Google; API semantics in `../gemini/errors.md`.
