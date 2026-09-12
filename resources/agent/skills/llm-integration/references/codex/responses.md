# Codex / ChatGPT — Responses wire (SSE + WebSocket)

Streaming inference on the Codex provider base. **Never mix hosts and auth.**

Sources: [endpoint/responses.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/responses.rs),
[responses_websocket.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/responses_websocket.rs),
[sse/responses](https://github.com/openai/codex/tree/main/codex-rs/codex-api/src/sse) (if present).

| Auth | Base | Typical headers |
|---|---|---|
| ChatGPT session token | `https://chatgpt.com/backend-api/codex` | `Authorization: Bearer <ChatGPT-token>`, `ChatGPT-Account-ID`, `originator`, `User-Agent`; optional `x-codex-installation-id` |
| OpenAI API key | `https://api.openai.com/v1` | `Authorization: Bearer $OPENAI_API_KEY`; optional `OpenAI-Organization` / `OpenAI-Project` |

Relative paths (appended to base):

| Route | Path | Role |
|---|---|---|
| Responses | `/responses` | Normal user-owned inference |
| Guardian | `/guardian` | Full Guardian approval-review agent |
| GuardianClassifier | `/guardian-classifier` | Lightweight async Guardian risk classification |

SSE: `POST {base}{path}` with `Accept: text/event-stream`, `Content-Type: application/json`.
WebSocket: same path with `https`→`wss` / `http`→`ws` (e.g. `wss://chatgpt.com/backend-api/codex/responses`). Auth headers on the upgrade. Optional permessage-deflate.

Session correlation (HTTP): `session-id`, `thread-id`, `x-client-request-id`; subagent turns may send `x-openai-subagent`. Turn state may round-trip via response / WS header `x-codex-turn-state`.

Auth / OAuth / account selection: [chatgpt-backend.md](chatgpt-backend.md). Legacy unary compact: [compact.md](compact.md). Public OpenAI Responses: `../openai/responses.md` (different product contract).

## Positive case — SSE turn

```bash
curl -N https://chatgpt.com/backend-api/codex/responses \
  -H "Authorization: Bearer $CHATGPT_TOKEN" \
  -H "ChatGPT-Account-ID: $CHATGPT_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{
    "model": "<slug>",
    "instructions": "You are a coding assistant.",
    "input": [{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
    "store": false,
    "stream": true,
    "include": ["reasoning.encrypted_content"]
  }'
```

Expect SSE frames until `response.completed`. Close before that = stream failure (retryable transport), not success.

## Request contract

Codex clients default **`store: false`**, **`stream: true`** (stateless replay via opaque items).

```json
{
  "model": "<slug>",
  "instructions": "<system>",
  "input": [ /* ResponseItem[] */ ],
  "tools": [ /* raw tool schemas */ ],
  "tool_choice": "auto",
  "parallel_tool_calls": true,
  "reasoning": { "effort": "…", "summary": "…" },
  "store": false,
  "stream": true,
  "include": ["reasoning.encrypted_content"],
  "service_tier": "…",
  "prompt_cache_key": "…",
  "text": { "verbosity": "…" },
  "client_metadata": { "session_id": "…", "thread_id": "…", "turn_id": "…" },
  "access_programs": { }
}
```

**Input item tags (common):** `message`, `reasoning`, `function_call`,
`function_call_output`, `web_search_call`, `compaction`, and request-only
`compaction_trigger`. Opaque `encrypted_content` on reasoning/compaction must
be copied byte-for-byte — never decode, truncate, log, or synthesize.

Optional body compression: zstd on the HTTP transport when the client enables it.

## Response / stream contract

**HTTP response headers (before/with SSE):** parse rate windows from
`x-codex-primary-used-percent` / `-window-minutes` / `-reset-at` (and
`x-codex-secondary-*`); other metered families use `x-{limit}-primary-*`
(underscores → hyphens). Credits: `x-codex-credits-has-credits`,
`-unlimited`, `-balance`. Also: `openai-model`, `x-reasoning-included`,
`X-Models-Etag`, `x-request-id`, `x-codex-turn-state`,
`x-codex-promo-message`, `x-codex-rate-limit-reached-type`,
`x-codex-safety-buffering-*`.

**SSE / WS event `type` values (consume until completed):**

| Event | Meaning |
|---|---|
| `response.created` | Response id |
| `response.output_item.added` / `.done` | Item lifecycle |
| `response.output_text.delta` | Assistant text delta |
| `response.reasoning_*.delta` / `.done` | Reasoning stream |
| `response.custom_tool_call_input.delta` | Tool arg streaming |
| `response.completed` | **Terminal success** — id, usage, optional `end_turn` |
| `response.failed` / `error` / `response.incomplete` | Terminal failure |
| `codex.rate_limits` | Mid-stream rate snapshot (WS / Codex events) |
| `codex.response.metadata` / `response.metadata` | Headers/metadata (model, turn state, moderation, safety) |

Ignore unknown `*.delta` / progress events. Usage lives on
`response.completed.response.usage` (`input_tokens`, `output_tokens`,
`total_tokens`, cached/cache-write details, optional
`codex_rollout_budget_units`).

## WebSocket workflow

1. Connect to `wss://…{path}` with the same auth/session headers as HTTP.
2. Upgrade may set `x-reasoning-included`, `openai-model`, `x-codex-turn-state`.
3. Send one text frame: Responses body + `"type":"response.create"` (optional
   `previous_response_id`, `generate: false` for warmup).
4. Read text JSON events with the same decoder as SSE until
   `response.completed`. Binary frames are invalid.
5. Idle timeout applies to send and receive (default provider
   `stream_idle_timeout_ms` = **300_000**). Connection lifetime cap ~**60
   minutes** → `websocket_connection_limit_reached` (retryable: open a new
   socket). `previous_response_not_found` is also retryable (resend full
   request).

## Remote v2 compaction (`compaction_trigger`)

Not OpenAI `context_management`. Same `POST /responses` (or WS) stream:

1. Copy retained history into `input`.
2. Append **last** item `{"type":"compaction_trigger"}` (control only — never
   persist in history).
3. Stream normally (`store: false`, `stream: true`).
4. Require `response.completed` and **exactly one** output item with
   `type: "compaction"`. Other outputs allowed; wrong compaction count =
   fatal. Close before completed = retryable (cap retries ≤ 2 for compact).
5. Rebuild history: keep eligible user text messages (drop developer wrappers /
   assistant/tool per v2 filter), truncate newest-first to ~64k text-token
   budget, append the opaque compaction item last.

Details / legacy `POST /responses/compact`: [compact.md](compact.md).

## Workflow

1. Pick **one** base+auth pair; load headers from [chatgpt-backend.md](chatgpt-backend.md).
2. Prove SSE with a minimal user message and `stream: true`.
3. Decode by `type`; treat `response.completed` as the only success end.
4. Replay opaque reasoning/compaction blobs; request
   `include: ["reasoning.encrypted_content"]` when `store: false`.
5. Surface `x-codex-*` rate headers / `codex.rate_limits` to the UI.
6. Use idle timeouts that **reset on every chunk** and avoid a short fixed
   total-request deadline for long reasoning. Add a configurable product-level
   deadline when the caller needs one.
7. Enable WebSocket only when `supports_websockets` and the provider allows it
   (AWS SigV4 providers do not).
8. For context pressure, prefer remote v2 trigger over inventing local summary
   text when the feature is on.

## Edge cases

- Guardian routes share the Responses request/SSE/WS machinery; only the path
  changes.
- Mid-stream `codex.rate_limits` updates usage UI without ending the turn.
- Safety buffering may appear on deltas / metadata (`safety_buffering`,
  `x-codex-safety-buffering-*`).
- Compact streams run longer — keep a smaller retry budget than normal turns.
- Do not treat comment/`:` SSE lines or unknown event types as completion.

## Errors

| Signal | Handling |
|---|---|
| HTTP non-2xx before SSE | Map status/body; honor Retry-After on 429 with a capped sleep |
| `response.failed` codes | `rate_limit_exceeded`, context-window, quota, cyber/misalignment/bio policy, `invalid_prompt`, overloaded → typed errors; many others retryable |
| Stream closed before `response.completed` | Retryable network/stream error |
| WS wrapped `{type:"error", status, error, headers}` | Same as HTTP status; connection-limit / previous-response-not-found → reconnect/retry |
| Compaction ≠ 1 output compaction | Non-retryable provider error |

## Related

- [chatgpt-backend.md](chatgpt-backend.md) — host, OAuth, ChatGPT headers
- [compact.md](compact.md) — legacy `/responses/compact` vs v2 trigger
- [README.md](README.md) — Codex guide router
- `../openai/responses.md` — public platform Responses (do not mix)
