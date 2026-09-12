# Codex — Remote compaction (legacy compact vs v2)

Two remote-compaction paths exist on the ChatGPT Codex backend. They are
**not** interchangeable with OpenAI public `context_management` compaction.

| Path | Endpoint | Transport | Client |
|---|---|---|---|
| **Legacy** | `POST …/responses/compact` | Unary JSON | `CompactClient` (`codex-api`) |
| **Remote v2** | `POST …/responses` | SSE stream | Normal Responses client + `compaction_trigger` |

Auth / base URL: [chatgpt-backend.md](chatgpt-backend.md). Full Responses SSE
and item wire shapes for v2: [responses.md](responses.md) — do not re-parse
SSE here.

## When to use which path

| Situation | Use |
|---|---|
| Provider `remote_compaction = V2` **and** `RemoteCompactionV2` feature on | **v2** (`/responses` + trigger) |
| Same provider capability but feature **off** | **Legacy** `/responses/compact` |
| Provider unsupported | Local compaction (out of scope here) |

Prefer **v2** for new work when the feature flag is available: it preserves
opaque compaction checkpoints and harness metadata links; legacy returns a
provider-normalized transcript without stable envelope linkage.

## Positive case — legacy `POST /responses/compact`

`ModelClient::compact_conversation_history` builds a Responses-shaped body,
then `CompactClient::compact_input` POSTs it to `responses/compact`.

Request body (`CompactionInput`):

```json
{
  "model": "<slug>",
  "input": [ /* ResponseItem history */ ],
  "instructions": "<resolved compaction instructions>",
  "tools": null,
  "parallel_tool_calls": true,
  "reasoning": { "effort": "…", "summary": "…" },
  "service_tier": "…",
  "prompt_cache_key": "…",
  "text": { },
  "access_programs": { "cyber": "standard" }
}
```

Optional fields omit when empty/`None`. **API-key auth omits `service_tier`.**

Response:

```json
{ "output": [ /* Vec<ResponseItem> replacement transcript */ ] }
```

May set response header `x-codex-turn-state` (sticky routing); clients store it
for the turn. Timeout ≈ `stream_idle_timeout × 4` (full unary wait, not idle).

Empty `input` → client returns `[]` without calling the network.

## Positive case — remote v2 (pointer)

Build a normal streaming Responses request (`store: false`, `stream: true`)
and append **`{"type":"compaction_trigger"}` as the last input item** (request
control only — never durable history). See [responses.md](responses.md) for
SSE framing.

Success contract (Codex remote v2 / `compact_remote`):

1. Stream must reach `response.completed` (early close → retryable).
2. Exactly **one** output item with `type: "compaction"` (wrong count → fatal).
3. Other output items allowed; ignore non-compaction for the result.
4. Keep `encrypted_content` opaque — never decrypt/validate.
5. Rebuild history client-side: retain real user messages (drop developer /
   instruction wrappers; assistant/tool not retained by the v2 filter),
   truncate retained text to **64 000** tokens from the end, append the
   compaction item last. Drop the trigger before rebuild.
6. Cap stream retries at **2** (compact runs longer than normal turns).

## Edge cases

- Do **not** call `/responses/compact` from a v2-enabled session path — parity
  tests assert v2 never hits that URL.
- Legacy output still runs `should_keep_compacted_history_item` (drop developer /
  wrapper users; keep assistant / compaction items).
- Mid-turn vs standalone differ on initial-context injection (session owner),
  not on the HTTP shape above.
- Rate-limit (429) on v2 streams is **not** retried; transport/5xx may be.
- Never synthesize compaction items or truncate `encrypted_content`.

## Related

- [responses.md](responses.md) — `/responses` SSE, `compaction_trigger`, items
- [chatgpt-backend.md](chatgpt-backend.md) — host, auth, headers
- Sources: [core/compact.rs](https://github.com/openai/codex/blob/main/codex-rs/core/src/compact.rs),
  [compact_remote_history.rs](https://github.com/openai/codex/blob/main/codex-rs/core/src/compact_remote_history.rs),
  [compact_remote_v2.rs](https://github.com/openai/codex/blob/main/codex-rs/core/src/compact_remote_v2.rs);
  Responses wire: [endpoint/responses.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/responses.rs)
  (`compaction_trigger` on `/responses`)
