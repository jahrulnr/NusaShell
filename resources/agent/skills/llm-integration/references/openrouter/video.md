# OpenRouter — Video (async)

Async video: submit → poll → download. Generation takes ~30s to minutes — tell
the user the job was submitted.

Auth: `OPENROUTER_API_KEY` — [openrouter.ai/keys](https://openrouter.ai/keys).

**Not ZDR-eligible** — provider must retain output briefly for async download.

Upstream: [openrouter-video](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-video).
Docs: [Video generation](https://openrouter.ai/docs/guides/overview/multimodal/video-generation).

Prefer over OpenAI Videos/Sora (deprecated) — see `../openai/video.md`.

## Three steps

1. `POST /api/v1/videos` → `{ id, polling_url, status: "pending" }`
2. `GET <polling_url>` ~every 30s until `completed`  
   Terminal failures: `failed` \| `cancelled` \| `expired` — surface `error`
3. Download MP4: prefer content URL from job **with auth**, or documented
   `unsigned_urls` / content endpoint for the job id

```bash
# Download pattern (auth required on content fetch)
curl -sS -L -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  "https://openrouter.ai/api/v1/videos/{id}/content?index=0" \
  --output out.mp4
```

## Discover model capabilities (do not guess)

`resolution`, `aspect_ratio`, `duration`, `frame_images[].frame_type` are
per-model. Fetch before first submit:

```bash
curl -sS https://openrouter.ai/api/v1/videos/models \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  | jq '.data[] | select(.id == "google/veo-3.1")'
```

Note: `supported_resolutions`, `supported_aspect_ratios`, `supported_sizes`,
`supported_durations` (often discrete `[4,6,8]`), `supported_frame_images`,
`generate_audio` / `seed` bools, `pricing_skus`,
`allowed_passthrough_parameters`. Out-of-set values → **400**.

Also: [model-list.md](model-list.md) / models with video output modality.

## Positive case — submit → poll

```bash
curl -sS -X POST https://openrouter.ai/api/v1/videos \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "google/veo-3.1",
    "prompt": "a golden retriever playing fetch on a sunny beach"
  }'
# → id, polling_url, status

# Poll:
curl -sS "$POLLING_URL" -H "Authorization: Bearer $OPENROUTER_API_KEY"
# loop until status=completed; on failed|cancelled|expired print .error
```

## Request contract

**Required:** `model`, `prompt`.

| Field | Notes |
|---|---|
| `duration` | Must be in `supported_durations` |
| `resolution` / `aspect_ratio` / `size` (`WxH`) | `size` ↔ resolution+aspect |
| `generate_audio` | Only if model capability true |
| `seed` | Only if capability true |
| `callback_url` | HTTPS webhook instead of/in addition to poll |
| `frame_images[]` | Image-to-video: `{ type:"image_url", image_url:{url}, frame_type:"first_frame"\|"last_frame" }` |
| `input_references[]` | Style reference; same entry shape, no `frame_type`. If both present, **`frame_images` wins** |
| `provider.options.<slug>.parameters` | Passthrough — keys from `allowed_passthrough_parameters`; semantics from **upstream** provider docs |

Image `url`: public `https://` or data URL
`data:image/png;base64,…`.

### Provider passthrough example

```json
{
  "model": "google/veo-3.1",
  "prompt": "a time-lapse of a flower blooming",
  "provider": {
    "options": {
      "google-vertex": {
        "parameters": {
          "personGeneration": "allow",
          "negativePrompt": "blurry, low quality"
        }
      }
    }
  }
}
```

Casing varies (camelCase vs snake_case) by upstream provider.

## Webhooks (optional)

Pass `callback_url` (HTTPS). On terminal state OpenRouter POSTs
`video.generation.{completed,failed,cancelled,expired}`.

| Header | Meaning |
|---|---|
| `X-OpenRouter-Idempotency-Key` | `<job_id>-<status>` |
| `X-OpenRouter-Signature` | If signing configured: `t=<ts>,v1=<hmac>` — HMAC-SHA256 of `<ts>,<raw_body>`; reject old timestamps (~5m) |

## Edge cases

- Async delay is normal — communicate submitted state.
- Never invent resolution/duration — read model caps.
- Auth header required on content download even when URL looks signed.
- Prefer OpenRouter video for **new** work vs deprecated OpenAI Sora.

## Errors

| Situation | Action |
|---|---|
| 400 on submit | Param not in supported_* sets |
| Terminal `failed`/`expired` | Show `.error`; do not poll forever |
| 401/402/429 | [errors.md](errors.md) |

## Related

- [model-list.md](model-list.md)
- [images.md](images.md) — still frames / edits
- [generations.md](generations.md) — when `generation_id` present on job
- [README.md](README.md)
- [Models filter](https://openrouter.ai/models?output_modalities=video)
