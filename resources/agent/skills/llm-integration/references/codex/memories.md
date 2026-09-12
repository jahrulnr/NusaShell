# Codex — Memories (`trace_summarize`)

Unary (non-streaming) memory summarization for normalized conversation
traces. Client: `MemoriesClient` in
[endpoint/memories.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/memories.rs).
Types: `MemorySummarizeInput` / `MemorySummarizeOutput` / `RawMemory` in
[common.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/common.rs).

**This module exposes a single HTTP path.** Prefer `summarize_input` over
raw `summarize` so wire renames stay correct.

## Positive case

```http
POST {base_url}/memories/trace_summarize
Content-Type: application/json
Authorization: <provider auth headers>

{
  "model": "gpt-test",
  "traces": [
    {
      "id": "trace-1",
      "metadata": { "source_path": "/tmp/trace.json" },
      "items": [
        { "type": "message", "role": "user", "content": [] }
      ]
    }
  ]
}
```

```json
{
  "output": [
    {
      "trace_summary": "raw summary",
      "memory_summary": "memory summary"
    }
  ]
}
```

Rust: `MemoriesClient::summarize_input(&MemorySummarizeInput { … }, headers)`
→ `Vec<MemorySummarizeOutput>`. Path string is exactly
`memories/trace_summarize` (joined onto the provider `base_url`; core
documents the full route as `/v1/memories/trace_summarize` when the base
already includes `/v1`).

## Contract

| Layer | Field | Wire / notes |
|---|---|---|
| Request | `model: String` | required |
| Request | `raw_memories: Vec<RawMemory>` | serialized as **`traces`** |
| Request | `reasoning: Option<Reasoning>` | omitted when `None`; `{ effort?, summary?, context? }` |
| Trace | `id`, `metadata.source_path`, `items: Vec<Value>` | `items` are normalized response/protocol items (JSON) |
| Response | wrapper | `{ "output": [ … ] }` — client returns only the vector |
| Output | `raw_memory: String` | wire **`trace_summary`**, alias **`raw_memory`** |
| Output | `memory_summary: String` | required |

Client helpers:

- `summarize(body: Value, headers)` — POST arbitrary JSON; parse `output`.
- `summarize_input(&MemorySummarizeInput, headers)` — encode typed input,
  then `summarize`.
- `with_telemetry(…)` — optional request telemetry on the session.

Auth, retries, and header merge go through `EndpointSession` + `Provider`
(same as other codex-api endpoints). Extra headers (e.g. originator /
subagent) are caller-supplied.

## Edge cases

- **Empty traces:** API client will still POST; `ModelClient::summarize_memories`
  short-circuits to `Ok([])` before calling when `raw_memories` is empty.
- **Encode failure:** `summarize_input` → `ApiError::Stream("failed to encode…")`.
- **Malformed response body:** JSON parse failure → `ApiError::Stream(…)`.
- **Output field rename:** servers may send `trace_summary` or legacy
  `raw_memory`; both map to `MemorySummarizeOutput.raw_memory`.
- **Optional `reasoning`:** skip-serializing; core passes `effort` only when
  a resolved reasoning effort is present (`summary` / `context` left `None`).
- **No streaming / no other verbs** in this module — unary POST only.

## Related

- Compaction (history shrink, not memory summarize): [compact.md](compact.md)
- Responses / items shape for `traces[].items`: [responses.md](responses.md)
- Auth / base URL: [chatgpt-backend.md](chatgpt-backend.md)
- Source: [endpoint/memories.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/memories.rs),
  [common.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/common.rs) (`MemorySummarize*`, `RawMemory*`),
  caller: [core/client.rs](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs) (`summarize_memories`)
