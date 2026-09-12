# Gemini — generateContent (native chat)

Primary text/agent surface.

- Endpoint: `POST https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`
- Stream: `POST …/models/{model}:streamGenerateContent?alt=sse` → [stream.md](stream.md)
- Auth: `x-goog-api-key: $GEMINI_API_KEY` (or `$GOOGLE_API_KEY`)
- Docs: https://ai.google.dev/gemini-api/docs/generate-content/text-generation

## Positive case

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.6-flash:generateContent" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [
      {"role": "user", "parts": [{"text": "Say hello in one short sentence."}]}
    ],
    "generationConfig": {
      "maxOutputTokens": 256,
      "thinkingConfig": {"thinkingLevel": "low"}
    }
  }'
```

## Request contract

```json
{
  "contents": [
    {"role": "user", "parts": [{"text": "..."}]}
  ],
  "systemInstruction": {"parts": [{"text": "system / developer instructions"}]},
  "tools": [{"functionDeclarations": []}],
  "toolConfig": {"functionCallingConfig": {"mode": "AUTO"}},
  "generationConfig": {
    "temperature": 1.0,
    "topP": 0.95,
    "maxOutputTokens": 65535,
    "stopSequences": [],
    "responseMimeType": "text/plain",
    "thinkingConfig": {}
  },
  "safetySettings": []
}
```

| Field | Notes |
|---|---|
| `contents[].role` | `user` or `model` only (not `assistant` / `system`) |
| `contents[].parts` | `text`, `inlineData` (`mimeType`+`data`), `fileData`, `functionCall`, `functionResponse`, thought parts |
| `systemInstruction` | Top-level; not a `contents` role |
| `generationConfig.maxOutputTokens` | **Set explicitly.** Native API uses a low internal default when omitted → early `MAX_TOKENS` truncation (agent loops break). Current text models commonly allow up to **65535**. |
| `thinkingConfig` | See [thinking.md](thinking.md) — Gemini 2.5 vs 3 differ |
| `tools` / `toolConfig` | See [tools.md](tools.md) |

## Response contract

```json
{
  "candidates": [{
    "content": {
      "role": "model",
      "parts": [
        {"text": "...", "thought": true, "thoughtSignature": "..."},
        {"text": "visible answer"},
        {"functionCall": {"name": "…", "args": {}}, "thoughtSignature": "…"}
      ]
    },
    "finishReason": "STOP",
    "safetyRatings": []
  }],
  "usageMetadata": {
    "promptTokenCount": 0,
    "candidatesTokenCount": 0,
    "totalTokenCount": 0,
    "thoughtsTokenCount": 0
  }
}
```

`finishReason` (common): `STOP` | `MAX_TOKENS` | `SAFETY` | `RECITATION` |
`OTHER`. Map for OpenAI-shaped loops: STOP→stop, MAX_TOKENS→length,
SAFETY/RECITATION→content_filter.

## Workflow

1. Prove with curl using `x-goog-api-key` (Bearer often fails on this host).
2. Pin a model id from [model-list.md](model-list.md); re-check `GET /models`.
3. Always set `maxOutputTokens` for agent/tool turns.
4. If tools: sanitize schemas + replay thought signatures ([tools.md](tools.md), [thinking.md](thinking.md)).
5. Track `usageMetadata` (include `thoughtsTokenCount` when present).

## Edge cases

- **Role alternation**: consecutive same-role `contents` are invalid — merge
  adjacent same-role parts (required for parallel tool results).
- **Gemini 3 + temperature**: keep defaults; forcing high temperature can
  loop or degrade reasoning (Google guidance).
- **Gemma** models reject `thinkingConfig` with 400 — omit it entirely.
- **Strip OpenRouter prefixes**: native ids are `gemini-3.6-flash`, not
  `google/gemini-3.6-flash` (Vertex may keep `google/` — [vertex.md](vertex.md)).
- **Free tier**: too small for multi-call agent turns — see [errors.md](errors.md).

## Error handling

See [errors.md](errors.md). Never blind-retry 400 schema / thought-signature
failures.
