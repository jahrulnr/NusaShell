# OpenAI — Image generation & editing

Dedicated Image API (not chat completions). Auth: `Authorization: Bearer $OPENAI_API_KEY`.

Prefer **Image API** for one-shot generate/edit. Prefer **Responses API
`image_generation` tool** for multi-turn conversational editing (see
`responses.md` + `tools.md`).

## Positive case — generate

```bash
curl https://api.openai.com/v1/images/generations \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-image-2",
    "prompt": "A children'\''s book drawing of a baby otter with a stethoscope",
    "n": 1,
    "size": "1024x1024",
    "quality": "high"
  }'
```

Decode `data[0].b64_json` → PNG/WebP bytes and write to disk.

## Request contract — `POST /v1/images/generations`

JSON body:

| Field | Required | Notes |
|---|---|---|
| `model` | yes (for GPT Image) | Prefer `gpt-image-2`. Also: `gpt-image-1.5`, `gpt-image-1`, `gpt-image-1-mini`. Legacy `dall-e-2`/`dall-e-3` are deprecated. |
| `prompt` | yes | Text description |
| `n` | no | Images per request (default 1). Multiplies cost. |
| `size` | no | GPT Image: `1024x1024`, `1024x1536`, `1536x1024`, or `auto`. `gpt-image-2` also accepts flexible sizes within its constraints. |
| `quality` | no | `low` \| `medium` \| `high` \| `auto` |
| `background` | no | `transparent` \| `opaque` \| `auto` (model-dependent; transparent usually needs `png`/`webp`) |
| `output_format` | no | `png` \| `webp` \| `jpeg` |
| `output_compression` | no | 0–100 (when format supports it) |
| `response_format` | DALL·E only | `url` \| `b64_json`. **Unsupported for GPT Image** — GPT Image always returns `b64_json`. |

## Positive case — edit (multipart)

```bash
curl https://api.openai.com/v1/images/edits \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -F model=gpt-image-2 \
  -F prompt="Replace the background with a soft studio backdrop" \
  -F image=@source.png \
  -F mask=@mask.png
```

- Source image + optional mask: same format/size, typically **&lt; 50 MB**.
- Mask: transparent pixels = editable region (standard OpenAI mask convention).

## Response contract

```json
{
  "created": 1748372400,
  "data": [{ "b64_json": "<base64>" }],
  "usage": {
    "input_tokens": 12,
    "output_tokens": 1056,
    "total_tokens": 1068,
    "input_tokens_details": { "text_tokens": 12, "image_tokens": 0 }
  }
}
```

- GPT Image: images are **base64** in `data[]` — decode before storing.
- Token `usage` is GPT Image–specific (image + text tokens). Track it for cost.

## Responses API image tool (when needed)

Use when the user iterates on an image inside a chat turn:

```json
{
  "model": "gpt-5.4",
  "input": "Generate a gift basket photo…",
  "tools": [{ "type": "image_generation", "action": "generate" }]
}
```

- Mainline model must support the tool; the tool picks a GPT Image model.
- `action`: `auto` (default) \| `generate` \| `edit`.
- Bills **mainline tokens + image tokens** — more expensive than Image API alone.

## Workflow

1. Confirm org can call GPT Image (Organization Verification may be required).
2. Prefer `gpt-image-2` for new work; pin a snapshot if the product needs stability.
3. Generate or edit → decode `b64_json` → save with the matching extension.
4. Record `usage` tokens for cost accounting.
5. Invalid `size`/`quality`/`background` combos → 400; fix params, never blind-retry.

## Edge cases

- **Org verification**: unverified orgs get 403/`permission_error` on GPT Image — verify in the developer console before blaming the request body.
- **`response_format=url` on GPT Image** → 400. Always handle `b64_json`.
- **`n > 1`** multiplies cost and payload size; cap client-side for user-facing apps.
- **Transparent + jpeg** is invalid — use `png` or `webp` with `background: "transparent"`.
- **Model drift / deprecations**: `dall-e-2`/`dall-e-3` shut down earlier; older GPT Image aliases (`gpt-image-1`, `gpt-image-1-mini`, …) have announced shutdown windows — migrate toward `gpt-image-2` and re-check `GET /v1/models` before shipping.
- **Edits without multipart** fail — do not send JSON with embedded base64 unless the SDK documents that shape; native API is multipart for files.
- Empty `data[]` after 200 is a provider bug/guardrail edge — treat as hard failure, surface message from `error` if present.

## Error handling

400 = bad param/combo (read `error.param` / message); 403 = verification/access;
429 = IPM/TPM — see `errors.md`. Never retry a 400 body unchanged.
