# Bedrock — ConverseStream (binary event-stream)

Streaming surface for the Converse API. **This is NOT SSE.** Bedrock streams
use AWS binary event-stream framing (`application/vnd.amazon.eventstream`),
the same wire format as Kinesis / Transcribe streaming. An SSE parser will
silently produce garbage. Verified 2026-09-11.

- Endpoint: `POST https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/converse-stream`
- Auth: SigV4 (service `bedrock`) **or** `Authorization: Bearer $AWS_BEARER_TOKEN_BEDROCK`
- Content-Type (request): `application/json` (the request body is JSON; only
  the **response** body is binary event-stream)
- Docs: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseStream.html

## Endpoint + auth

Same as [converse.md](converse.md) but the path ends in `/converse-stream`.
The request body is identical to Converse. The response is a binary stream of
frames, each carrying one event.

## Request contract

Identical to [converse.md](converse.md) request. Add nothing stream-specific
to the body (no `stream: true` flag — the path selects streaming).

## Response contract — binary event-stream framing

The response body is a sequence of binary frames. Each frame:

```
[ total-length (4 bytes, big-endian) ]
[ headers-length (4 bytes) ]
[ prelude CRC (4 bytes) ]
[ headers ... ]   -- length-prefixed key/value pairs
[ payload ... ]   -- JSON bytes for this event
[ message CRC (4 bytes) ]
```

Mandatory headers on every frame:

| Header | Meaning |
|---|---|
| `:message-type` | `event` for data frames; `error` for error frames |
| `:event-type` | Event kind (see below) when `:message-type` == `event` |
| `:message-id` | Optional sequence id |
| `:content-type` | `application/json` for the payload |

**Do not hand-roll the decoder.** Use a library that implements the AWS
event-stream binary format:
- Python: `botocore.eventstream.EventStreamBuffer` + `EventStreamJSONParser`
  (this is what LiteLLM uses).
- JS/TS: `@smithy/eventstream-codec` / `@aws-sdk/client-bedrock-runtime`
  (handles framing + parsing automatically).
- Rust: `aws-smithy-eventstream` / `branchforge` codec.

The decoded payload of each frame is a JSON object with exactly one top-level
key naming the event. Event order per response:

```
messageStart            (once)
  for each content block (indexed by contentBlockIndex):
    contentBlockStart   (tool use only)
    contentBlockDelta   (one or more: text / reasoningContent / toolUse)
    contentBlockStop
messageStop             (once; carries stopReason)
metadata                (once; carries usage + metrics)
```

### Event shapes

| Event | Payload | Notes |
|---|---|---|
| `messageStart` | `{"role": "assistant"}` (also `conversationId` on some models) | First event. |
| `contentBlockStart` | `{"contentBlockIndex": N, "start": {"toolUse": {"toolUseId": "...", "name": "..."}}}` | Tool-use blocks only. Text/reasoning blocks skip straight to delta. |
| `contentBlockDelta` | `{"contentBlockIndex": N, "delta": {"text": "..." | "toolUse": {"input": "..."} | "reasoningContent": {"text": "...", "signature": "..."}}}` | The incremental content. `toolUse.input` is a **partial JSON string** — accumulate per `contentBlockIndex`, parse at `contentBlockStop`. |
| `contentBlockStop` | `{"contentBlockIndex": N}` | Block complete. Parse accumulated tool-input JSON here. |
| `messageStop` | `{"stopReason": "end_turn", "additionalModelResponseFields": {}}` | Terminal. `stopReason` same values as Converse. |
| `metadata` | `{"usage": {"inputTokens": N, "outputTokens": N, "totalTokens": N, ...}, "metrics": {"latencyMs": N}}` | Token usage arrives **here**, not in messageStop. |

### Error frames

An error frame has `:message-type: error` and a payload like
`{"__type": "modelStreamErrorException", "message": "..."}`. These can arrive
**mid-stream** (after some content deltas). Common stream-only exceptions:
`modelStreamErrorException` (424), `throttlingException` (429),
`internalServerException` (500), `validationException` (400),
`modelTimeoutException` (408). See [errors.md](errors.md).

## Workflow

1. POST the Converse-shaped body to `/converse-stream` with SigV4/bearer auth.
2. Feed raw response bytes into an event-stream buffer incrementally
   (`EventStreamBuffer.add_data(chunk)`). Do not buffer the whole body.
3. For each decoded frame, parse the JSON payload and dispatch on the event key.
4. Accumulate `contentBlockDelta` per `contentBlockIndex`:
   - `text` -> append to current text block.
   - `toolUse.input` -> append to a per-index string buffer.
   - `reasoningContent` -> append text; capture `signature` (replay later).
5. On `contentBlockStop`, JSON-parse the accumulated tool-input string.
6. On `messageStop`, record `stopReason`.
7. On `metadata`, record `usage` (this is the only place usage appears).
8. Use an **idle timeout that resets on every frame**, not a total-request
   timeout (reasoning models can stream for minutes).

## Edge cases

- **No `[DONE]` sentinel.** The stream ends when the TCP body closes or an
  error frame arrives. `messageStop` is the logical end of content; `metadata`
  is the logical end of the response.
- **Usage is only in `metadata`, not in `messageStop`.** If you stop reading
  at `messageStop` you will miss token accounting.
- **Tool input is partial JSON across deltas.** Never `json.loads` a single
  `toolUse.input` delta; accumulate until `contentBlockStop`.
- **`contentBlockStart` is omitted for text/reasoning blocks** — only tool-use
  blocks emit it. Index text/reasoning blocks by the `contentBlockIndex` on
  their deltas.
- **Error frames mid-stream**: a stream can deliver partial text, then an
  error frame. Surface already-received content + the error; do not discard
  partial output unless your UX requires atomic delivery.
- **Refusal-safe streaming (Claude Fable/Mythos/Sonnet 5)**: some Claude
  models on Bedrock can refuse **after** emitting partial text. OpenClaw
  buffers the whole stream until `messageStop` proves the response is safe
  before exposing any text to the user. Consider this for user-facing chat on
  those models.
- **Fake streaming**: some models/regions do not truly stream; LiteLLM detects
  this and fakes a stream from a non-streaming Converse response
  (`fake_stream`). If you observe the whole body arriving at once, your model
  may not support real streaming — fall back to Converse and chunk the text.

## Error handling

See [errors.md](errors.md). Stream error frames with `__type`
`modelStreamErrorException` / `throttlingException` / `internalServerException`
are retryable (re-send the whole request; Bedrock streams are not resumable).
`validationException` mid-stream is not retryable as-is.
