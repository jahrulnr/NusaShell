# Codex — `GET /models`

ChatGPT Codex catalog (not public OpenAI `api.openai.com/v1/models`).
Base: `https://chatgpt.com/backend-api/codex` → path `models`.

Sources: [endpoint/models.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/models.rs),
[protocol/openai_models.rs](https://github.com/openai/codex/blob/main/codex-rs/protocol/src/openai_models.rs)
(`ModelsResponse` / `ModelInfo`); on-disk cache in Codex CLI/core.

## Positive case

```bash
curl -sS "https://chatgpt.com/backend-api/codex/models?client_version=0.99.0" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "ChatGPT-Account-ID: $CHATGPT_ACCOUNT_ID" \
  -D - -o /tmp/codex-models.json
# Expect 200, optional ETag, body {"models":[...]} with slug / context_window / …
jq '.models[0] | {slug, display_name, context_window, supported_in_api}' \
  /tmp/codex-models.json
```

Codex client always appends `client_version` (`ModelsClient::request_url`).

## Contract — Codex vs OpenAI v1

| | Codex backend | OpenAI `GET /v1/models` |
|---|---|---|
| URL | `…/backend-api/codex/models` | `https://api.openai.com/v1/models` |
| Auth | ChatGPT access token (+ account id) | Platform API key |
| Query | **`client_version`** (required by client; catalog may gate on it) | none for listing |
| Envelope | `{ "models": ModelInfo[] }` | `{ "object":"list", "data": Model[] }` |
| Identity | `slug` | `id` |
| Payload | Rich agent catalog | Minimal: `id`, `object`, `created`, `owned_by` |
| Headers | May return **`ETag`** (clients cache + revalidate) | no catalog ETag contract in Codex client |

### Codex `ModelInfo` (high-signal fields)

`slug`, `display_name`, `description`, `default_reasoning_level`,
`supported_reasoning_levels[]`, `shell_type`, `visibility`, `supported_in_api`,
`priority`, `context_window`, `max_context_window`,
`effective_context_window_percent`, `truncation_policy`,
`supports_image_detail_original`, `experimental_supported_tools`,
`input_modalities`, upgrade / verbosity / service-tier metadata, etc.

Wire may also emit legacy `base_instructions` for older clients (serde helpers
on `ModelsResponse`).

Resolved runtime window (Codex core): `context_window.or(max_context_window)`.

## `models_cache` behavior

Codex CLI/core writes `~/.codex/models_cache.json` (`ModelsCacheEntry`):

- `fetched_at`, optional `etag`, optional `client_version`, `models[]`
- **TTL** default **300s**; load returns miss if stale or
  `client_version` ≠ current client
- Matching **ETag** can **refresh TTL** without rewriting the catalog body
- Cache errors are non-fatal → refetch `/models`

Use cached `slug` → `context_window` (fallback `max_context_window` when
`context_window` ≤ 0). Missing/unreadable cache → refetch (no hard fail).

## Edge cases

- **Do not treat OpenAI `/v1/models` as Codex-compatible** — different host,
  auth, envelope (`data` vs `models`), and almost no capability fields.
- **Codex is capability-rich**: reasoning efforts, context windows, tool/shell
  hints, visibility/`supported_in_api` — use these instead of docs-only
  guessing required for OpenAI’s minimal list.
- Omitting `client_version` diverges from the official client; version
  mismatch invalidates on-disk cache.
- Account/tier gating: catalog is ChatGPT-account-aware (`auth.json` /
  selected account).
- Prefer cache `context_window` over third-party ceilings (often smaller than
  models.dev).

## Related

- Auth / base URL / headers → [chatgpt-backend.md](chatgpt-backend.md)
- Router → [README.md](README.md)
- Public OpenAI minimal catalog → [`../openai/model-list.md`](../openai/model-list.md)
