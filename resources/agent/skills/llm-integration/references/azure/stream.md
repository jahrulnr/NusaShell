# Azure OpenAI - Streaming (SSE)

Azure streams as Server-Sent Events with **parity to public OpenAI** framing:
`Content-Type: text/event-stream`, blocks separated by blank lines, `data:`
lines carrying JSON, `data: [DONE]` sentinel for Chat Completions. Enable with
`stream: true`.

Verified 2026-09-11 (Azure OpenAI reference; LiteLLM `llms/azure/` streaming
handlers reuse OpenAI chunk assembly).

## Endpoint + auth

Same endpoints as `chat-completions.md` / `responses.md` with `stream: true` in
the body. Auth unchanged (`api-key` or `Authorization: Bearer`). `api-version`
still required on the date path.

## Request contract (streaming flag)

```json
{
  "model": "my-gpt5-deployment",
  "messages": [{"role": "user", "content": "..."}],
  "stream": true,
  "stream_options": {"include_usage": true}
}
```

- `stream_options.include_usage: true` is required to receive a final usage
  chunk (otherwise usage is absent on streams).
- Responses API: set `stream: true`; events are typed (see below).

## Response contract (chunk shapes)

Chat Completions chunks (identical to public OpenAI):

```
data: {"choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}
data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: {"choices":[],"usage":{...}}
data: [DONE]
```

- Azure may add `content_filter_results` on chunks (per-choice) and
  `prompt_filter_results` on the first chunk - parse defensively.
- Tool-call fragments arrive in `delta.tool_calls[]` (`index`, `id`,
  `function.name`, `function.arguments` partial strings).

Responses API event sequence (same as public OpenAI):

```
response.created
  -> response.output_item.added -> response.content_part.added
     -> response.output_text.delta (repeat)
  -> response.output_item.done
response.completed            (carries final usage + full output)
```

- Function calls stream via `response.function_call_arguments.delta` / `.done`.
- Failure paths: `response.failed`, `response.error`, `response.incomplete` -
  a stream can end without `response.completed`.

## Workflow

1. Parse exactly as public OpenAI (`../openai/stream.md`) - same parser rules.
2. Treat `data: [DONE]` (Chat) / `response.completed` (Responses) as the only
   commit point for usage and finish state.
3. Close on the terminal event - do not wait for server EOF.
4. EOF without a terminal event = failure (surface partial text + error).

## Edge cases

- **Content filter mid-stream**: a 200 OK stream can be cut off by the output
  filter; the terminal chunk carries `finish_reason: "content_filter"` with
  partial `content`. Treat as truncated, not a clean stop.
- **Mid-stream error events**: a 200 OK does not guarantee success; errors can
  arrive as SSE events after headers - handle both pre-byte HTTP errors and
  in-stream error events.
- **`x-request-id` / `apim-request-id`**: Azure returns `apim-request-id`
  (and sometimes `x-request-id`) - log it for support correlation.
- **No resumable streams**: SSE auto-reconnect replays nothing; restart the
  request (stateless) or use Responses `previous_response_id` (verify `store`
  support first). Do not assume resumable streams.
- Proxy/LB buffering can batch deltas - flush per event, not per TCP packet.

## Error handling

- HTTP error before first byte (status != 200): parse the Azure error envelope
  (`errors.md`); retry only transient (429/5xx).
- Mid-stream error event: stop, surface partial output + error, do not retry
  the partial generation blindly (side effects may have fired).
- **Timeouts** - reasoning models at high effort can stream for tens of minutes.
  For interactive streams, use a short connect timeout + an idle timeout that
  resets on every received chunk, plus a configurable product-level deadline
  when needed. See cross-provider rule #2 in `../../SKILL.md`.
