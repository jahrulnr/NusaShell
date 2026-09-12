# Anthropic — Streaming (SSE)

Set `"stream": true` on `POST /v1/messages` to receive named server-sent
events. SDKs are recommended for accumulation, but a direct integration handles
the events itself (below).

## Event flow

```text
message_start
├── content_block_start (index 0)
│   ├── content_block_delta …   (text_delta / thinking_delta / input_json_delta)
│   └── content_block_stop
├── content_block_start (index 1)
│   └── …
├── message_delta               (stop_reason + cumulative usage)
└── message_stop
```

`ping` events can appear anywhere. New event types may be added — ignore
unknown types gracefully (versioning policy).

## Event reference

| Event | Payload | Notes |
|---|---|---|
| `message_start` | `message` with empty `content` + `usage.input_tokens` | Shell of the final Message |
| `content_block_start` | `index`, `content_block` (`text`/`thinking`/`tool_use`/…) | `tool_use` opens with empty `input` |
| `content_block_delta` | `index`, `delta` (table below) | Many per block |
| `content_block_stop` | `index` | Block complete |
| `message_delta` | `delta.stop_reason`, `stop_sequence`, `usage` | **Usage is cumulative** |
| `message_stop` | — | Stream complete |
| `error` | `error.type` / `message` | Mid-stream failure (e.g. `overloaded_error`) |
| `ping` | — | Keep-alive |

### Delta types

| Delta `type` | Field | Assemble by |
|---|---|---|
| `text_delta` | `text` | Concatenate (incremental, not cumulative) |
| `input_json_delta` | `partial_json` | Concatenate then `JSON.parse` at `content_block_stop` — the final `tool_use.input` is an object, deltas are partial strings |
| `thinking_delta` | `thinking` | Concatenate into the block's (summarized) thinking text |
| `signature_delta` | `signature` | Single event just before `content_block_stop`; store verbatim on the thinking block |
| `citations_delta` | `citation` | Append to the text block's `citations` |

With `display: "omitted"` no `thinking_delta` events are emitted — a thinking
block opens, gets one `signature_delta`, and closes (faster time-to-text).
Treat any block with non-empty thinking text under `display: "updates"` (beta)
as a progress update — see [thinking.md](thinking.md).

## Sample trace (text only)

```sse
event: message_start
data: {"type":"message_start","message":{"id":"msg_…","content":[],"usage":{"input_tokens":25,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}
```

## Workflow

1. Prove the non-streaming call first; then flip `stream: true` and print raw
   events before writing parser code.
2. Dispatch on event `type`; keep per-`index` block accumulators (text, partial
   JSON, thinking + signature).
3. On `message_delta`, record `stop_reason` and cumulative `usage`; on
   `message_stop`, emit the finished message.
4. Keep an idle timeout that resets on **every** event (thinking phases can run
   minutes with no text output).
5. If you don't need incremental handling, accumulate to the final message
   (SDK `.get_final_message()` / `.finalMessage()`); for large `max_tokens`
   values, prefer streaming or a provider-supported background/batch flow and
   keep a product-level deadline.

## Edge cases

- `message_delta` usage is **cumulative**, not per-event — read the last one.
- Tool blocks: `input_json_delta` chunks arrive only after a complete key/value
  is formed; expect pauses between chunks. For lower latency on parameter
  values, enable per-tool `eager_input_streaming` (fine-grained streaming).
- Thinking + streaming: text can arrive "chunky" (batched) especially for
  thinking — expected.
- `message_start` may carry an `input_transformations` array (beta
  `thinking-binding-controls-2026-08-01`) reporting blocks the API dropped;
  a server-side fallback inserts a `fallback` content block at model
  boundaries.
- A stream can fail **after** HTTP 200: handle the `error` event (shape below),
  don't assume network-level errors only.

```sse
event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}
```

## Error recovery

- **Claude 4.6 and later:** capture the partial response, then continue with a
  **user** message (`"Your previous response was interrupted … Continue from
  where you left off."`).
- **Claude 4.5 and earlier:** the same capture-and-resume, but the partial text
  goes into a trailing **assistant** message (prefill-style continuation).
- Only text blocks can be resumed mid-block; `tool_use` and thinking blocks are
  all-or-nothing — restart them. Prefer SDK accumulation helpers.

## Next

Stop reasons once the stream ends → [messages.md](messages.md).
Signature/preservation rules for thinking → [thinking.md](thinking.md).
Retry policy for HTTP-level failures → [errors.md](errors.md).
