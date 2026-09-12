# OpenRouter — Chat completions

Primary chat surface: `POST https://openrouter.ai/api/v1/chat/completions`
(OpenAI-shaped). Auth: `Authorization: Bearer $OPENROUTER_API_KEY`.

Also supports OpenAI Responses-compatible paths on the same host when needed;
prefer one convention per adapter/request path. Attribution headers recommended:
`HTTP-Referer`, `X-Title`.

## Positive case

```bash
curl https://openrouter.ai/api/v1/chat/completions \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H "Content-Type: application/json" \
  -H "HTTP-Referer: https://your-app.example" \
  -H "X-Title: Your App" \
  -d '{
    "model": "anthropic/claude-sonnet-4",
    "messages": [
      {"role": "user", "content": "Say hello in one sentence."}
    ]
  }'
```

## Request contract (OpenRouter-specific extras)

Standard OpenAI chat fields plus:

| Field | Notes |
|---|---|
| `model` | Full slug (`provider/model` or with `:variant`) |
| `models` | Fallback chain — try next on failure |
| `provider` | Routing prefs: `order`, `only`, `ignore`, `sort`, quantizations, `allow_fallbacks` |
| `route` | e.g. `"fallback"` |
| `transforms` | Optional prompt transforms |
| `usage` | Response usage metadata; do not send the deprecated `{ "include": true }` request flag |
| `plugins` | e.g. web search plugins |
| Multimodal parts | `image_url`, **`video_url`** (unlike OpenAI), `input_audio` where supported |

Pin production models to exact slugs from `model-list.md`. Check
`supported_parameters` before sending `temperature`, `tools`, `reasoning`, etc.

## Response contract

OpenAI-like `choices[].message` + `usage`. With fallbacks, the served model/
provider may differ from the requested primary — read response model fields
and optionally confirm via `generations.md`.

## Content parts vs dedicated APIs

| Need | Use |
|---|---|
| Chat with image/video **input** | content parts here (`video_url` OK) |
| Generate image/audio/video **output** | `images.md` / `stt.md` / `tts.md` / `video.md` — not chat |

## Edge cases

- Short model names without provider → 404.
- Unsupported param for routed provider → 400 (stricter than “ignore unknown”).
- `:free` variants — hard rate caps; not for production SLA.
- Reasoning models may reject `temperature` — see `supported_parameters`.

## Related

- Streaming deviations → `stream.md`
- Tools + provider routing → `tools.md`
- Errors / 402 / fallbacks → `errors.md`
