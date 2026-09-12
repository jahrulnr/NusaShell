# Gemini — Streaming (SSE)

Enable streaming with:

```text
POST /v1beta/models/{model}:streamGenerateContent?alt=sse
Accept: text/event-stream
x-goog-api-key: $GEMINI_API_KEY
```

Body = same JSON as [generate-content.md](generate-content.md).
`?alt=sse` is required for Server-Sent Events framing.

## Event shape

Each SSE `data:` line is a partial GenerateContentResponse (JSON), not an
OpenAI Chat Completions delta:

```
data: {"candidates":[{"content":{"parts":[{"text":"Hel"}],"role":"model"}}]}

data: {"candidates":[{"content":{"parts":[{"text":"lo"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{…}}
```

- Text accumulates across chunks in `candidates[].content.parts[].text`.
- Tool calls may arrive as incremental `functionCall` parts — buffer by
  call identity until the part is complete.
- Thought parts (`"thought": true`) and `thoughtSignature` may appear mid-
  stream — preserve them for the next request ([thinking.md](thinking.md)).
- There is **no** OpenAI-style `data: [DONE]` sentinel on the native path;
  end-of-stream is connection close after the final candidate chunk (often
  carrying `finishReason` + `usageMetadata`).

## Parser rules (language-agnostic)

1. Buffer until `\n\n`; parse `data:` JSON; ignore `:` comment lines.
2. Accumulate part text / functionCall args in arrival order.
3. Commit usage from the last chunk that carries `usageMetadata`.
4. **Idle timeout that resets on every chunk** — avoid a short fixed total
   timeout for interactive streams; add a configurable product-level deadline
   when the caller needs one.
5. HTTP ≠ 200 before first byte → error envelope ([errors.md](errors.md));
   mid-stream JSON with `error` / blocked candidate → treat as failure.
6. On client abort, cancel the upstream body or generation continues billing.

## Edge cases

- Empty first chunks while the model thinks are normal when thinking is on —
  do not treat silence under the idle budget as success.
- `finishReason: MAX_TOKENS` mid-tool-call → truncated JSON args; fail the
  call and raise `maxOutputTokens` (do not execute partial args).
- Translating to OpenAI-shaped stream chunks for an agent loop is fine;
  keep native thought signatures in side-channel metadata so they can be
  replayed.

## Error handling

Same retry policy as [errors.md](errors.md). A 200 with a blocked /
empty candidate is still a hard failure for the caller.
