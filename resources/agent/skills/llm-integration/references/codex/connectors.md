# Codex — Connectors directory & app batch

ChatGPT backend connectors surface (not `api.openai.com`). Base:
`https://chatgpt.com/backend-api` (`config.chatgpt_base_url`). Auth/shared
headers: [chatgpt-backend.md](chatgpt-backend.md). Codex sends
`OAI-Product-Sku: codex` (POST may override via `apps_mcp_product_sku`;
default remains `codex`).

Sources: [connectors/lib.rs](https://github.com/openai/codex/blob/main/codex-rs/connectors/src/lib.rs),
[chatgpt/connectors.rs](https://github.com/openai/codex/blob/main/codex-rs/chatgpt/src/connectors.rs),
[chatgpt_client.rs](https://github.com/openai/codex/blob/main/codex-rs/chatgpt/src/chatgpt_client.rs).

## Positive cases

```bash
BASE=https://chatgpt.com/backend-api
# Token + account: chatgpt-backend.md / codex login

curl -sS "$BASE/connectors/directory/list?external_logos=true" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "OAI-Product-Sku: codex" \
  -H "Content-Type: application/json"

# Next page — URL-encode token; empty/whitespace token ends paging
curl -sS "$BASE/connectors/directory/list?token=<nextToken>&external_logos=true" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "OAI-Product-Sku: codex" \
  -H "Content-Type: application/json"

# Workspace accounts only; Codex fetches in parallel with list
curl -sS "$BASE/connectors/directory/list_workspace?external_logos=true" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "OAI-Product-Sku: codex" \
  -H "Content-Type: application/json"

curl -sS -X POST "$BASE/ps/apps/batch" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "OAI-Product-Sku: codex" \
  -H "Content-Type: application/json" \
  -d '{"app_ids":["alpha"],"include_tools":true}'
```

## Contract

### Shared headers

| Header | Value |
|---|---|
| `Authorization` | Bearer ChatGPT access token (Codex backend auth) |
| `OAI-Product-Sku` | `codex` |
| `Content-Type` | `application/json` |
| (+ default / auth-provider headers) | See chatgpt-backend.md |

Requires Codex backend auth and a ChatGPT **account id** (re-login if missing).

### `GET /connectors/directory/list`

| Query | Notes |
|---|---|
| `external_logos=true` | Always set by Codex |
| `token` | Optional cursor; percent-encoded |

Response (`DirectoryListResponse`): `{ "apps": [DirectoryApp], "next_token": null }`.
`nextToken` aliases `next_token`. App fields use snake_case or camelCase aliases:
`id`, `name`, `description`, `appMetadata`, `branding`, `labels`, `logoUrl`,
`logoUrlDark`, `iconAssets`, `iconDarkAssets`, `distributionChannel`, `visibility`.

### `GET /connectors/directory/list_workspace`

Same response shape. No client pagination. Only when
`auth.is_workspace_account()`; merged with public `list` (dedupe by `id`).

### `POST /ps/apps/batch`

Body: `{ "app_ids": ["…"], "include_tools": true }`.

Codex projection (other Plugin Service fields ignored):

```json
{
  "apps": [{
    "id": "alpha", "name": "Alpha", "description": "…",
    "icon_url": null, "icon_dark_url": null, "distribution_channel": null,
    "tools": [{
      "name": "search", "title": "Search", "description": "…",
      "is_enabled": true, "disabled_reason": null, "is_read_only": false
    }]
  }]
}
```

`icon_url_dark` aliases `icon_dark_url`. Missing tool `is_enabled` ⇒ `true`.
Timeouts: directory GET 60s; batch POST 60s.

## Client behavior (Codex)

1. Page `list` until `next_token` null/blank; drop `visibility: "HIDDEN"`.
2. Workspace: start `list_workspace` + `list` together; workspace errors soft.
3. Merge duplicate ids (fill empty name/description/logos/branding/metadata).
4. Normalize names; install URL `{chatgpt_origin}/apps/{slug}/{id}`.
5. Cache directory ~1h (memory + disk) by base URL + account + user + workspace
   flag. Batch uses a separate store; only requested ids are committed.

## Edge cases

- **OAuth scopes:** login asks
  `openid profile email offline_access api.connectors.read api.connectors.invoke`.
  Missing `api.connectors.*` fails directory/batch even if Responses works.
- **Not platform API-key auth** — ChatGPT-token / Codex backend only
  (chatgpt-backend.md).
- **Non-workspace** — skip `list_workspace`; do not invent a workspace merge.
- **Hidden apps** — never appear in the merged list.
- **Batch extras** — full actions/runtime may be present; Codex keeps display
  metadata (+ optional tool summaries) only.
- **Unknown ids** — batch may omit them → `missing_app_ids`.
- **Product sku** — GET hardcodes `codex`; POST uses config override when set.
  Prefer `codex` unless you own that config.

## Error handling

Non-2xx: `Request failed with status {code}: {body}`. Missing auth/account id
fails before HTTP. Retry after re-auth or backoff on transient 5xx — do not
loop on 401/403 scope failures.
