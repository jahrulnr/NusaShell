# Codex — Standalone web search

`POST {base}/alpha/search` runs Codex backend search (web + related
commands). Not OpenAI Platform `/v1/*`. Default base:
`https://chatgpt.com/backend-api/codex`. Auth is a ChatGPT access token.

Sources: `SearchClient` / `SearchRequest` in
[endpoint/search.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/search.rs),
[search.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/search.rs);
tool caller [ext/web-search](https://github.com/openai/codex/tree/main/codex-rs/ext/web-search).

## Positive case

Minimal query-only body:

```bash
curl -sS https://chatgpt.com/backend-api/codex/alpha/search \
  -H "Authorization: Bearer $CHATGPT_ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -H "originator: codex_cli_rs" \
  -H "ChatGPT-Account-ID: $CHATGPT_ACCOUNT_ID" \
  -H "session-id: $SESSION_ID" \
  -H "thread-id: $SESSION_ID" \
  -H "x-client-request-id: $SESSION_ID" \
  -d '{
    "id": "conversation-1",
    "model": "gpt-5-codex",
    "commands": { "search_query": [{ "q": "latest Go release" }] }
  }'
```

Success shape:

```json
{
  "encrypted_output": null,
  "output": "fresh results",
  "results": [
    {
      "type": "text_result",
      "ref_id": "turn0search0",
      "url": "https://example.com/go",
      "title": "Go",
      "snippet": "Go result"
    }
  ]
}
```

## Request contract

| Field | Required | Notes |
|---|---|---|
| `id` | yes | Session / conversation id (also used as default session header) |
| `model` | yes | Codex model id for the turn |
| `commands` | no | Operations bag; omit → empty commands |
| `commands.search_query[]` | common | `{ "q": "…" }` plus optional `recency` (days), `domains` |
| `commands.image_query[]` | no | Same shape as `search_query` |
| `commands.open[]` | no | `{ "ref_id", "lineno?" }` — ref id or URL |
| `commands.click` / `find` / `screenshot` | no | Page ops after open |
| `commands.finance` / `weather` / `sports` / `time` | no | Structured lookups |
| `commands.response_length` | no | `short` \| `medium` \| `long` |
| `input` | no | Plain string **or** Responses-style `ResponseItem[]` (messages/images) |
| `settings` | no | Location, context size, domain filters, image settings, callers, web access |
| `reasoning` | no | Optional reasoning config |
| `max_output_tokens` | no | Cap on returned output |

`settings` highlights: `user_location.type=approximate`,
`search_context_size` (`low`/`medium`/`high`), `filters.allowed_domains` /
`blocked_domains`, `image_settings`, `allowed_callers`
(`direct`/`shell`/`code_interpreter`), `external_web_access` (`true`/`false`
or `cached`/`indexed`/`live`).

## Response contract

| Field | Notes |
|---|---|
| `output` | Required string — model-facing summary / tool text |
| `encrypted_output` | Optional ciphertext companion; may be `null` |
| `results` | Optional opaque JSON objects (forward-compatible). Common `type`: `text_result` (`ref_id`, `url`, `title`, `snippet`), also `image_result`, etc. |

Treat unknown `results[].*` keys as passthrough. Prefer `results` for
structured hits; `output` for the agent-visible string.

## Auth headers

| Header | Role |
|---|---|
| `Authorization: Bearer …` | ChatGPT access token (required) |
| `ChatGPT-Account-ID` | Selected workspace/account when multi-account |
| `originator` | Client id — Codex CLI uses `codex_cli_rs` |
| `User-Agent` | Codex-style UA string |
| `x-codex-installation-id` | Installation routing (optional) |
| `session-id` / `thread-id` / `x-client-request-id` | Correlate turn; often set from the request `id` |
| `Content-Type` / `Accept` | `application/json` |

`SearchClient` also merges provider auth headers + optional extras
(e.g. turn metadata, originator override). Token refresh stays outside the
wire client.

## Edge cases

- Path is `alpha/search` under the Codex base — if the client base ends in
  `/responses`, strip that suffix before appending `/alpha/search`.
- Older backends may omit `results` entirely (`null`/absent) vs send `[]`;
  both are valid — do not treat missing as empty without checking.
- `results` items are intentionally untyped JSON; new `type` values must not
  break parsers (keep opaque / ignore unknown). `text_result` is the common
  hit shape; other types may still appear in the raw payload.
- Per-request account id may override the client default account header.
- Non-2xx: treat as failure; redact token from error bodies; keep body reads
  bounded.
- Typical client timeout ≈ 60s; search is not SSE.

## Related

- Auth / base URL → [chatgpt-backend.md](chatgpt-backend.md)
- Codex Responses streaming: same base host, path `/responses` —
  [responses.md](responses.md)
- Upstream: [endpoint/search.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/search.rs),
  [search.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/search.rs),
  [ext/web-search](https://github.com/openai/codex/tree/main/codex-rs/ext/web-search)
