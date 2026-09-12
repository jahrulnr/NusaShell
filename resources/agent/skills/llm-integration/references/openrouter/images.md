# OpenRouter — Images (generate / edit)

Dedicated Image API: `POST /api/v1/images` (not chat completions with image
parts). Discover models/params first to avoid 400s.

Auth: `OPENROUTER_API_KEY` for generate/edit.
Discovery endpoints may be public.

Upstream: [openrouter-images](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-images).
Docs: [Image generation](https://openrouter.ai/docs/guides/overview/multimodal/image-generation).

## Decision tree

| Need | Approach |
|---|---|
| Which image models / params | Discover via models + per-model endpoints |
| Text → image | `POST /api/v1/images` with prompt |
| Edit existing image | Same POST with `input_references` (model must accept image input) |

Default model often used upstream: `google/gemini-3.1-flash-image-preview`
(confirm live catalog).

## Discover capabilities

List image-capable models from the catalog (filter modalities / image APIs as
documented live). For one model’s providers and exact params:

```bash
# Pattern from upstream skill — confirm path against live OpenAPI if 404
curl -sS "https://openrouter.ai/api/v1/images/models/{author}/{slug}/endpoints" \
  -H "Authorization: Bearer $OPENROUTER_API_KEY"
```

Per endpoint, note: `provider_name` / `provider_slug`, `supported_parameters`
(with allowed values), `allowed_passthrough_parameters`, `supports_streaming`,
`pricing`. Absent param ⇒ unsupported — do not send it.

Readable capability strings: enums `1K | 2K | 4K`, ranges `0–100`, bools
`supported`.

Also use [model-list.md](model-list.md) /
`architecture.input_modalities` including `image` for edit support.

## Positive case — generate

```bash
curl -sS -X POST https://openrouter.ai/api/v1/images \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "google/gemini-3.1-flash-image-preview",
    "prompt": "a red panda wearing sunglasses",
    "aspect_ratio": "16:9"
  }'
```

### Response shape

Images return base64 in `data[]`. Raster PNG often omits `media_type`; vector
(SVG) includes it:

```json
{
  "created": 1748372400,
  "data": [{ "b64_json": "<base64-encoded image data>" }],
  "usage": {
    "prompt_tokens": 0,
    "completion_tokens": 4175,
    "total_tokens": 4175,
    "cost": 0.04
  }
}
```

Decode `b64_json` to a file; prefer extension from `media_type` when present.

## Positive case — edit

Send source image as image-to-image reference (`input_references`). Model
`input_modalities` must include `image`. Supported source types typically:
`.png` `.jpg` `.jpeg` `.webp` `.gif` (as data URL or URL per API).

```bash
# Illustrative — field names follow Image API guide; verify against OpenAPI
B64=$(base64 < photo.png | tr -d '\n')
curl -sS -X POST https://openrouter.ai/api/v1/images \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{
    \"model\": \"google/gemini-3.1-flash-image-preview\",
    \"prompt\": \"make the sky purple\",
    \"input_references\": [{
      \"type\": \"image_url\",
      \"image_url\": { \"url\": \"data:image/png;base64,${B64}\" }
    }]
  }"
```

## Common request parameters

Only send params the target endpoint supports (from discovery):

| Param | Notes |
|---|---|
| `model` | Image model id |
| `prompt` | Text instruction |
| `aspect_ratio` | e.g. `16:9`, `1:1`, `4:3` |
| `resolution` | Tier e.g. `512`, `1K`, `2K`, `4K` |
| `size` | Tier or `WxH` |
| `quality` | `auto` \| `low` \| `medium` \| `high` |
| `output_format` | `png` \| `jpeg` \| `webp` \| `svg` |
| `background` | `auto` \| `transparent` \| `opaque` |
| `output_compression` | 0–100 for webp/jpeg |
| `n` | 1–10 images when provider allows |
| `seed` | Deterministic where supported |
| `provider.options.<slug>` | Passthrough from `allowed_passthrough_parameters` |

Example passthrough:

```json
"provider": {
  "options": {
    "black-forest-labs": { "steps": 40, "guidance": 3 }
  }
}
```

## Edge cases

- Hardcoding unsupported flags → 400; discover first.
- Edit without image-capable model fails — check modalities.
- Multiple `n` → multiple `data[]` entries; save with suffixes.
- Cost may appear in `usage.cost` — surface it when presenting results.
- Contrast OpenAI Images: `../openai/images.md` (different hosts/params).

## Errors

| HTTP | Notes |
|---|---|
| 400 | Bad/unsupported param — re-check endpoints discovery |
| 401 / 402 / 429 | [errors.md](errors.md) |

## Related

- [model-list.md](model-list.md)
- [generations.md](generations.md) — if correlating cost by id
- [README.md](README.md)
- [Image generation guide](https://openrouter.ai/docs/guides/overview/multimodal/image-generation)
