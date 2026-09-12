# WHAM / PathStyle dual map

Account, task, usage, settings, and rate-limit-reset surfaces share one
`backend-client` API with two path prefixes. Same JSON contracts; only the
prefix changes.

**Auth:** ChatGPT-token (`Authorization: Bearer <access_token>`). Prefer
`ChatGPT-Account-Id` when a workspace is selected. Not platform API-key /
`api.openai.com`.

**Source of truth:** [backend-client/client.rs](https://github.com/openai/codex/blob/main/codex-rs/backend-client/src/client.rs)
(+ [rate_limit_resets.rs](https://github.com/openai/codex/blob/main/codex-rs/backend-client/src/client/rate_limit_resets.rs),
[thread_usage.rs](https://github.com/openai/codex/blob/main/codex-rs/backend-client/src/client/thread_usage.rs),
[chatgpt_turn_cost.rs](https://github.com/openai/codex/blob/main/codex-rs/backend-client/src/client/chatgpt_turn_cost.rs));
task GET also in [get_task.rs](https://github.com/openai/codex/blob/main/codex-rs/chatgpt/src/get_task.rs).

## PathStyle selection

| Style | When | Prefix |
|---|---|---|
| `ChatGptApi` | `base_url` contains `/backend-api` | `{base}/wham/…` |
| `CodexApi` | otherwise | `{base}/api/codex/…` |

Host normalization in `Client::with_http`: bare `https://chatgpt.com` or
`https://chat.openai.com` (no `/backend-api`) is rewritten to
`…/backend-api`, which selects `ChatGptApi`. Trailing slashes are trimmed.
`with_path_style` can override detection.

Canonical ChatGPT example base: `https://chatgpt.com/backend-api` → WHAM
URLs under `https://chatgpt.com/backend-api/wham/…`.

## Dual-path table

Suffix below is identical on both styles. Full URL =
`{base}{prefix}{suffix}`.

| Op | Method | Suffix | Notes |
|---|---|---|---|
| accounts/check | GET | `/accounts/check` | Plan / entitlement check |
| profiles/me | GET | `/profiles/me` | Token-usage profile |
| tasks/list | GET | `/tasks/list` | Query: `limit`, `task_filter`, `environment_id`, `cursor` |
| tasks/{id} | GET | `/tasks/{id}` | Task details; WHAM-only helper in `get_task.rs` |
| POST tasks | POST | `/tasks` | Create task; id from `task.id` or top-level `id` |
| sibling turns | GET | `/tasks/{id}/turns/{turn}/sibling_turns` | Turn attempts |
| config/bundle | GET | `/config/bundle` | Cloud-managed config bundle |
| settings/user | GET | `/settings/user` | Sends `Cache-Control: no-cache, no-store` |
| usage | GET | `/usage` | Rate-limit status (+ optional reset credits) |
| rate-limit-reset-credits | GET | `/rate-limit-reset-credits` | List reset credits |
| rate-limit-reset-credits/consume | POST | `/rate-limit-reset-credits/consume` | Body: `redeem_request_id`, optional `credit_id` |
| thread usage | POST | `/usage/thread_usage/query` | Body: `{ "thread_ids": ["…"] }` |
| thread turn cost | POST | `/usage/thread-estimates/query` | Body: threads + `include_settled_response_ids` |

Also dual-mapped (same prefix rule): `GET …/workspace-messages`,
`POST …/accounts/send_add_credits_nudge_email`.

### URL examples

```text
# ChatGptApi
GET  https://chatgpt.com/backend-api/wham/accounts/check
GET  https://chatgpt.com/backend-api/wham/tasks/list
POST https://chatgpt.com/backend-api/wham/tasks
GET  https://chatgpt.com/backend-api/wham/usage
POST https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume

# CodexApi  ({host} = configured Codex backend origin)
GET  {host}/api/codex/accounts/check
GET  {host}/api/codex/tasks/list
POST {host}/api/codex/tasks
GET  {host}/api/codex/usage
POST {host}/api/codex/rate-limit-reset-credits/consume
```

## Positive cases

1. **Detect style from base** — pass ChatGPT `…/backend-api` → all WHAM paths;
   pass a non-`backend-api` Codex host → all `/api/codex` paths. Do not
   hardcode one prefix when the other host is active.
2. **Accounts + usage** — `GET …/accounts/check` then `GET …/usage` for plan
   and rate-limit windows; optional header `x-openai-codex-luna-reserve: 1`
   only when the client can apply Luna Reserve.
3. **Tasks** — `GET …/tasks/list` → `GET …/tasks/{id}`; create with
   `POST …/tasks` and read created id from `task.id` (fallback `id`).
4. **Reset credits** — `GET …/rate-limit-reset-credits`, then
   `POST …/rate-limit-reset-credits/consume` with a fresh `redeem_request_id`
   (and `credit_id` when targeting a specific credit).
5. **Thread cost** — `POST …/usage/thread_usage/query` for thread totals;
   `POST …/usage/thread-estimates/query` for per-turn USD micros + settled
   response ids. Response must include the requested `thread_id`.

Minimal auth shape (env names only; never commit values):

```bash
curl -sS "$BASE/wham/accounts/check" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "ChatGPT-Account-Id: $CHATGPT_ACCOUNT_ID"
# CodexApi: replace /wham with /api/codex on $CODEX_BACKEND_HOST
```

## Edge cases

- **Wrong prefix on host** — `/api/codex` under `chatgpt.com/backend-api` (or
  `/wham` on a pure Codex API host) is not what `PathStyle` emits; expect
  404/auth failures.
- **Missing account id** — ChatGPT backend helpers require a ChatGPT account
  id (`codex login`); requests without it fail closed.
- **401** — treat as ChatGPT-token expiry / re-login; `RequestError::is_unauthorized`.
- **Create-task id** — success without `task.id` / `id` is a client error, not
  a silent empty result.
- **Thread usage miss** — if the response `threads` array omits the requested
  id, the client errors (do not invent zeros).
- **Settings cache** — always send `Cache-Control: no-cache, no-store` on
  `settings/user` so stale attribution flags are not reused.
- **API-key turn costs are not WHAM** — `turn_usage.rs` posts to
  `/v1/analytics/codex/turn-costs` (often remapped to `api.chatgpt.com`).
  That path is outside this dual map; see [analytics.md](analytics.md).
- **FedRAMP** — optional `X-OpenAI-Fedramp: true` when
  `with_fedramp_routing_header` is set.

## Related

- Auth / base URL / OAuth: [chatgpt-backend.md](chatgpt-backend.md)
- Responses / models (Codex `/codex` surface, not WHAM): [responses.md](responses.md),
  [models.md](models.md)
- Analytics turn costs (API-key path): [analytics.md](analytics.md)
- Router: [README.md](README.md)
