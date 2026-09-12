# OpenAI — Streaming (SSE)

Both APIs stream as Server-Sent Events: `Content-Type: text/event-stream`,
blocks separated by blank lines, each block = optional `event:` line + `data:`
line(s). Enable with `stream: true`.

## Chat Completions chunk shape

```
data: {"choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}
data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: {"choices":[],"usage":{...}}          // only with stream_options.include_usage
data: [DONE]
```

- Text arrives in `choices[].delta.content`; tool calls in
  `delta.tool_calls[]` fragments (`index`, `id`, `function.name`,
  `function.arguments` partial strings).
- Terminal `finish_reason` arrives on a chunk with empty `delta`.
- `data: [DONE]` is the sentinel — after it, the connection closes.

## Responses API event sequence

```
response.created
  → response.output_item.added        (per output item)
    → response.content_part.added
      → response.output_text.delta    (repeat: text fragments)
    → response.content_part.done
  → response.output_item.done
response.completed                     (carries final usage + full output)
```

- Function calls stream via `response.function_call_arguments.delta` / `.done`.
- Failure paths: `response.failed`, `response.error`, `response.incomplete` —
  a stream can end without `response.completed`.

## Parser rules (language-agnostic)

1. Buffer bytes until `\n\n`; split the block into lines. `data:` lines carry
   JSON; `event:` lines name the type (Responses API).
2. **Ignore lines starting with `:`** — comments/heartbeats.
3. Accumulate deltas strictly in arrival order; chunk boundaries are network
   artifacts, not token/word boundaries.
4. Treat the terminal event (`[DONE]` / `response.completed`) as the only
   commit point for usage and finish state.
5. **Close on the terminal event — do not wait for server EOF.** On the
   terminal event, stop consuming and close/cancel the response body
   (dropping the stream releases the connection). The reference
   implementation returns from its read loop on `response.completed` and
   never processes bytes after it. Do not rely on the server closing the
   connection first, and do not keep reading past the terminal event —
   trailing bytes are undefined.
6. **EOF without the terminal event is a failure, not a success.** The
   reference implementation errors with "stream closed before
   response.completed". For Chat Completions without `[DONE]`: if a terminal
   `finish_reason` chunk already arrived, the text is complete — accept it
   but flag missing usage (the usage chunk normally precedes `[DONE]`).
   Without any terminal `finish_reason`, treat as truncated: surface partial
   text + error. Never block forever waiting for a sentinel that may never
   come.
7. Handle **both** failure modes: HTTP error before the first byte (status !=
   200) and mid-stream error events after a 200 OK.
8. On client abort, close/cancel the upstream body — otherwise the connection
   and generation keep running.
9. **Timeouts — avoid a short fixed total timeout for interactive streams.**
   Reasoning models can think for tens of minutes at high effort / slow TPS; a
   fixed 30–60s timeout can kill a valid, already-billed generation. Use a
   short **connect** timeout plus an **idle timeout that resets on every
   received chunk**, and add a configurable product-level deadline when the
   caller needs one.

## Edge cases

- **Response headers worth logging**: `x-request-id` (support/debug
  correlation), `openai-model` (the model actually served — confirms alias
  resolution; log it with every response).
- **Usage details** on the terminal event:
  `usage.input_tokens_details.cached_tokens` (cache hits) and
  `cache_write_tokens` (GPT-5.6+) — needed for real cost accounting, not just
  `total_tokens`.
- Never JSON-parse a partial buffer; only parse complete `data:` payloads.
- Structured outputs streamed as text deltas are **not** valid JSON until the
  final event — buffer and parse once at the end, or use a partial-JSON
  parser only for UI preview.
- Reconnect logic: SSE auto-reconnect replays nothing; on reconnect, restart
  the request (stateless) or use the Responses API `previous_response_id`
  pattern — do not assume resumable streams.
- Proxy/load-balancer buffering can batch deltas — flush per event, not per
  TCP packet.
