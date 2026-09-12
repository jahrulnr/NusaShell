# Codex — Analytics (events / turn costs)

ChatGPT-backend telemetry for Codex. **Not** OpenRouter `/api/v1/analytics/*`
(management-key query API). Sources:
[analytics/client.rs](https://github.com/openai/codex/blob/main/codex-rs/analytics/src/client.rs),
[turn_usage.rs](https://github.com/openai/codex/blob/main/codex-rs/backend-client/src/client/turn_usage.rs).

**Auth:** ChatGPT-token (Codex backend). Typical headers:
`Authorization: Bearer <access_token>` + `ChatGPT-Account-ID: <account_id>`.
See [chatgpt-backend.md](chatgpt-backend.md). API-key auth is a narrow
side-path (plugin-scoped events / turn-costs only) — prefer ChatGPT-token.

Base for events is `chatgpt_base_url` (e.g. `https://chatgpt.com/backend-api`).

## Positive case — track events

```bash
curl -sS -X POST \
  "${CHATGPT_BASE_URL%/}/codex/analytics-events/events" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "ChatGPT-Account-ID: $CHATGPT_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "events": [
      {
        "event_type": "codex_skill_invoked",
        "event_params": { "thread_id": "…", "turn_id": "…", "…": "…" }
      }
    ]
  }'
```

Client: queue → reducer → `POST` JSON `{ "events": [...] }`, 10s timeout.
Disabled when `analytics_enabled == false`. Failures are best-effort warn/drop.

## Positive case — turn costs

```bash
# After host rewrite (see Edge cases): api.chatgpt.com, not chatgpt.com
curl -sS -X POST \
  "https://api.chatgpt.com/v1/analytics/codex/turn-costs" \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "ChatGPT-Account-ID: $CHATGPT_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -d '{ "turn_ids": ["turn-priced", "turn-pending"] }'
```

Optional provider scope (API-key path): forward only
`openai-organization` / `openai-project` from provider headers.

## Contract

### Events — request

| Field | Notes |
|---|---|
| `events` | Required array of track payloads |
| each item | Untagged union: `event_type` + `event_params` (e.g. turn, skill, plugin, hook, compaction, guardian, …) |

URL: `{base_url}/codex/analytics-events/events` (trailing `/` on base stripped).

Auth gate before send:

- No auth → no POST
- API-key → keep only events with `can_send_with_api_key_auth` (plugin-id–bearing)
- Non–Codex-backend auth → no POST
- ChatGPT-token / other Codex-backend modes → full batch

### Turn costs — request / response

| Field | Notes |
|---|---|
| request `turn_ids` | string[] |
| response `turns` | `ApiKeyTurnCost[]` |

Per turn:

| Field | Notes |
|---|---|
| `turn_id` | string |
| `status` | `pending` \| `priced` |
| `total_usd` | optional string decimal |
| `event_count` | optional u64 |
| `responses` | optional `{ response_id, total_usd }[]` |
| `model` / `speed` / `reasoning_effort` | optional strings |

Path always `/v1/analytics/codex/turn-costs` (query/fragment cleared).

## Edge cases — host rewrite (turn-costs)

`query_api_key_turn_costs` rewrites the **host** from `self.base_url`, then
sets the path:

| `base_url` host | Analytics host |
|---|---|
| `chatgpt.com` | `api.chatgpt.com` |
| `chat.openai.com` | `api.chatgpt.com` |
| `chatgpt-staging.com` | `api.chatgpt-staging.com` |
| anything else | unchanged |

Do **not** POST turn-costs to `chatgpt.com/.../backend-api/...`. Example:
`https://chatgpt.com/backend-api` → `https://api.chatgpt.com/v1/analytics/codex/turn-costs`.

`query_api_key_turn_costs_at(url, …)` skips rewrite (caller-owned URL).

Other:

- Full events queue (256) drops facts with a warn
- Debug capture file can disable network delivery
- Client auth headers win over conflicting provider auth on custom turn-cost URLs

## Related

- Auth / base URL: [chatgpt-backend.md](chatgpt-backend.md)
- Router: [README.md](README.md)
- **Not** OpenRouter analytics: `../openrouter/analytics.md`
