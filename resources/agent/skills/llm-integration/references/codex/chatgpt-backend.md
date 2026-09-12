# Codex — ChatGPT backend auth

OAuth + request-auth for the **ChatGPT Codex backend**
(`chatgpt.com/backend-api/codex`). Not public `api.openai.com/v1` + API key alone.

**Explicit compatibility reference only.** This documents an implementation
surface inferred from the upstream Codex client, not a general-purpose public
API contract. Use it only when direct backend compatibility is the stated task;
prefer public OpenAI/Codex APIs otherwise. Never ask a user to paste access
tokens, refresh tokens, or session cookies, and never log them.

Sources ([openai/codex](https://github.com/openai/codex)):
[login/server.rs](https://github.com/openai/codex/blob/main/codex-rs/login/src/server.rs),
[pkce.rs](https://github.com/openai/codex/blob/main/codex-rs/login/src/pkce.rs),
[device_code_auth.rs](https://github.com/openai/codex/blob/main/codex-rs/login/src/device_code_auth.rs),
[auth/](https://github.com/openai/codex/tree/main/codex-rs/login/src/auth)
(`manager`, `revoke`, `storage`, `token_data`, `personal_access_token`);
base URL [model-provider-info](https://github.com/openai/codex/blob/main/codex-rs/model-provider-info/src/lib.rs)
(`CHATGPT_CODEX_BASE_URL`); CF cookies
[chatgpt_cloudflare_cookies.rs](https://github.com/openai/codex/blob/main/codex-rs/http-client/src/chatgpt_cloudflare_cookies.rs);
product SKU [chatgpt_client.rs](https://github.com/openai/codex/blob/main/codex-rs/chatgpt/src/chatgpt_client.rs).

## Positive cases

### Authorize (browser PKCE S256)

```
GET https://auth.openai.com/oauth/authorize
  ?response_type=code
  &client_id=app_EMoamEEZ73f0CkXaXp7hrann
  &redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback
  &scope=openid%20profile%20email%20offline_access%20api.connectors.read%20api.connectors.invoke
  &code_challenge=<BASE64URL_SHA256(verifier)>
  &code_challenge_method=S256
  &state=<random>
  &id_token_add_organizations=true
  &codex_cli_simplified_flow=true
  &originator=codex_cli_rs
```

Listener: `http://127.0.0.1:1455/auth/callback` (fallback `1457`). Only that
redirect URI is allow-listed for this public `client_id`.

### Token exchange

```bash
curl -sS -X POST 'https://auth.openai.com/oauth/token' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=authorization_code&code=<code>&redirect_uri=http://localhost:1455/auth/callback&client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_verifier=<verifier>'
# → { "id_token", "access_token", "refresh_token" }
```

### Refresh (~1h access tokens)

```bash
curl -sS -X POST 'https://auth.openai.com/oauth/token' \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann","grant_type":"refresh_token","refresh_token":"<rt>"}'
```

### Revoke (logout, best-effort)

```bash
curl -sS -X POST 'https://auth.openai.com/oauth/revoke' \
  -H 'Content-Type: application/json' \
  -d '{"token":"<rt>","token_type_hint":"refresh_token","client_id":"app_EMoamEEZ73f0CkXaXp7hrann"}'
```

Prefer refresh token; else access token (`token_type_hint: access_token`, omit
`client_id`). Still delete local auth if revoke fails.

### Device-code flow

Issuer `https://auth.openai.com`:

| Step | Endpoint |
|---|---|
| User code | `POST /api/accounts/deviceauth/usercode` `{ "client_id" }` |
| Poll | `POST /api/accounts/deviceauth/token` `{ device_auth_id, user_code }` (≤15m; `403`/`404` = pending) |
| Verify UI | `https://auth.openai.com/codex/device` |
| Exchange | `/oauth/token` with `redirect_uri=…/deviceauth/callback` + server PKCE |

`404` on usercode → device login disabled; use browser PKCE.

### Authenticated Codex call (ChatGPT tokens)

```bash
curl -sS -N -X POST 'https://chatgpt.com/backend-api/codex/responses' \
  -H 'Authorization: Bearer <access_token>' \
  -H 'ChatGPT-Account-ID: <chatgpt_account_id>' \
  -H 'OAI-Product-Sku: codex' \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  -d '{"model":"<model>","input":"ping"}'
```

API-key mode: `https://api.openai.com/v1` + `Bearer sk-...` (no ChatGPT
account header). Do not cross-wire ChatGPT OAuth tokens ↔ platform API keys.

## Request/response contracts

### PKCE

Verifier: 64 random bytes → base64url no pad. Challenge:
`BASE64URL(SHA-256(verifier))` (`S256`).

### `$CODEX_HOME/auth.json` (ChatGPT login)

```json
{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "<jwt>",
    "access_token": "<jwt-or-opaque>",
    "refresh_token": "<opaque>",
    "account_id": "<chatgpt_account_id>"
  },
  "last_refresh": "<rfc3339>"
}
```

API-key-only: set `OPENAI_API_KEY`, omit/empty `tokens`. Optional:
`personal_access_token`, agent identity, Bedrock fields.

JWT claims under `https://api.openai.com/auth`: `chatgpt_account_id`,
`chatgpt_user_id`, `chatgpt_plan_type`, `chatgpt_account_is_fedramp`; email
top-level or under `https://api.openai.com/profile`.

### Backend request headers

| Header | When |
|---|---|
| `Authorization: Bearer …` | Always |
| `ChatGPT-Account-ID` | ChatGPT / PAT / agent identity |
| `OAI-Product-Sku` | ChatGPT backend helpers (default `codex`; MCP may use `X-OpenAI-Product-Sku`) |
| `originator` / User-Agent | Codex clients |
| CF allowlisted cookies | Shared jar for ChatGPT hosts — not auth |

Allowlist: `__cf_bm`, `__cflb`, `__cfruid`, `__cfseq`, `__cfwaitingroom`,
`_cfuvid`, `cf_clearance`, `cf_ob_info`, `cf_use_ob`. Hosts: `chatgpt.com`
(+ subdomains), `chat.openai.com`, `auth.openai.com`, `ab.chatgpt.com`,
`cdn.auth.openai.com`. Never store session/auth cookies.

### Base URL selection

| Auth mode | Default base |
|---|---|
| ChatGPT OAuth / tokens / PAT / agent identity / Headers | `https://chatgpt.com/backend-api/codex` |
| Platform API key | `https://api.openai.com/v1` |

Override only via explicit provider `base_url`. Path: `{base}/responses`.

## Workflow

1. PKCE → authorize → capture `code` on `:1455`.
2. Exchange → persist tokens + `chatgpt_account_id` from JWT.
3. Call Codex with Bearer + `ChatGPT-Account-ID` (+ SKU / CF jar).
4. Refresh before expiry; permanent refresh failure → re-login.
5. Logout: revoke (best-effort) → delete local auth.

Device-code: usercode → `/codex/device` → poll → exchange → same storage.

## Edge cases

- Redirect URI must be `http://localhost:1455/auth/callback` for this
  `client_id`; app UI handoff is post-callback only.
- Staging overrides: `CODEX_APP_SERVER_LOGIN_CLIENT_ID`,
  `CODEX_*_TOKEN_URL_OVERRIDE`.
- Forced workspace: `allowed_workspace_id` on authorize; reject mismatched
  `chatgpt_account_id`.
- CLI `auth.json` without expiry: refresh when a refresh token exists.
- Refresh may omit new `refresh_token` — keep previous.
- FedRAMP: may send `X-OpenAI-Fedramp: true`.
- PAT: `GET {authapi}/v1/user-auth-credential/whoami` Bearer
  (`https://auth.openai.com/api/accounts` default).

## Error handling

- Token exchange non-2xx: surface status + parsed error; never log tokens.
- Permanent refresh: `refresh_token_expired|reused|invalidated` → clear auth /
  re-login (no blind retry).
- Backend `401` with ChatGPT auth: refresh once, then fail closed.
- Device usercode `404`: fall back to browser PKCE.
- Cloudflare: reuse allowlisted cookies across clients; credentials stay in
  `Authorization`, never the cookie jar.

## Related files

- [README.md](README.md) — router
- [responses.md](responses.md), [models.md](models.md), [images.md](images.md),
  [search.md](search.md), [files.md](files.md), [compact.md](compact.md),
  [realtime.md](realtime.md), [wham.md](wham.md), [connectors.md](connectors.md),
  [analytics.md](analytics.md), [memories.md](memories.md)
- Public OpenAI: [`../openai/responses.md`](../openai/responses.md)
- OpenRouter OAuth: [`../openrouter/oauth.md`](../openrouter/oauth.md)
