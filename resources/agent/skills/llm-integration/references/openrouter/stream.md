# OpenRouter — Streaming (SSE)

`stream: true` on chat completions. For an interactive stream, use an
idle-timeout that resets on every chunk plus a product-level deadline; do not
apply this rule to unary or async endpoints.

Current OpenRouter responses include usage automatically when available; do not
add the deprecated `stream_options.include_usage` or `usage.include` flags.
See <https://openrouter.ai/docs/cookbook/administration/usage-accounting>.

## Positive case

```bash
curl -N https://openrouter.ai/api/v1/chat/completions \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o-mini",
    "stream": true,
    "messages": [{"role":"user","content":"Count to 5."}]
  }'
```

## Contract

- SSE lines: `data: {json}` … terminate with `data: [DONE]`
- Delta text in `choices[].delta.content` (and reasoning fields when present)
- Inspect `usage` on the terminal response/chunk when it is returned; do not
  rely on deprecated include flags.

## OpenRouter vs OpenAI usage chunk

| | OpenAI | OpenRouter |
|---|---|---|
| Final usage chunk | Often `choices: []` + `usage` | May include a choice with **empty delta** repeating `finish_reason` |

Parsers that assume “empty choices ⇒ usage-only” can mishandle OpenRouter —
accept either shape; key off presence of `usage`.

## Edge cases

- Mid-stream error events / non-200 after headers — treat as failure; don't
  mark the assistant message complete.
- Keepalive comments / blank lines — ignore.
- Fallback mid-stream is rare; if `models[]` fallbacks fire, generation metadata
  (`generations.md`) shows the provider chain after the fact.
- Proxy buffers that defeat SSE — disable response buffering for streaming routes.

## Error handling

Transport errors and 429 mid-stream → retry only if no side effects applied.
See `errors.md`.
