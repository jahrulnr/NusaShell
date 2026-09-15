# Gemini — Image generation & editing

Native image models (“Nano Banana” branding) generate images via either:

- **Interactions API** (`POST /v1beta/interactions`) — Generally Available
  since June 2026, recommended for all new projects, and the surface all
  new Google samples default to.
- **`generateContent`** with `responseModalities` including `IMAGE` — the
  legacy/agent-runtime path, still fully supported.

Pick one surface per integration and stick to it. Do not send OpenAI
`/v1/images/*` shapes to either.

Auth: `x-goog-api-key: $GEMINI_API_KEY`.
Docs (Interactions default): https://ai.google.dev/gemini-api/docs/image-generation
Interactions overview: https://ai.google.dev/gemini-api/docs/interactions-overview
Verified 2026-09-10 against both pages.

Root and discovery are shared with `generateContent`; see [README.md](README.md)
(do not use the `/v1beta2` path from the migration guide).

## When to use which

- **New code / greenfield** → Interactions API. It is where Google ships
  new features (multi-turn state, background execution, observable steps).
- **Matching Hermes / OpenClaw agent runtimes** → `generateContent`. Both
  runtimes still call `:generateContent` + `responseModalities`; do not mix
  the two in one code path.
- **Need `previous_interaction_id` multi-turn editing** → Interactions only.
- **Need Batch API, automatic function calling, explicit caching, or custom
  safety settings** → `generateContent` only (these are Interactions gaps as
  of the verified date).

## Positive case — Interactions API (current default)

```bash
curl -s -X POST \
  "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-image",
    "input": [
      {"type": "text", "text": "A watercolor otter holding a stethoscope"}
    ]
  }'
```

- `input` may be a plain string or an array of typed parts
  (`{"type":"text","text":...}`, `{"type":"image","mime_type":...,"data":...}`).
- Response is an `Interaction` object. Read the generated image via the
  `interaction.output_image` convenience property: `output_image.data` is
  base64 image bytes; `output_image.mime_type` is the image MIME type.

### Multi-turn editing via `previous_interaction_id`

```bash
# Turn 1: generate
resp1=$(curl -s -X POST ".../v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-3.1-flash-image","input":"Create a vibrant infographic about photosynthesis."}')
interaction_id=$(echo "$resp1" | jq -r .id)

# Turn 2: edit, referencing turn 1
curl -s -X POST ".../v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"gemini-3.1-flash-image\",
    \"input\": \"Translate the infographic to Spanish; change nothing else.\",
    \"previous_interaction_id\": \"$interaction_id\",
    \"response_format\": {
      \"type\": \"image\",
      \"mime_type\": \"image/jpeg\",
      \"aspect_ratio\": \"16:9\",
      \"image_size\": \"2K\"
    }
  }"
```

- `previous_interaction_id` carries conversation history only; `tools`,
  `system_instruction`, and `generation_config` are interaction-scoped and
  must be re-specified each turn.
- `response_format` fields: `type` (`"image"`), `mime_type`, `aspect_ratio`,
  `image_size` (`"512"`, `"1K"`, `"2K"`, `"4K"` — Lite is 1K only; Flash
  Image adds 512px).

### Reference images (up to 14)

```bash
curl -s -X POST ".../v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-image",
    "input": [
      {"type": "text", "text": "An office group photo of these people making funny faces."},
      {"type": "image", "mime_type": "image/png", "data": "<BASE64_1>"},
      {"type": "image", "mime_type": "image/png", "data": "<BASE64_2>"}
    ],
    "response_format": {"type": "image", "aspect_ratio": "5:4", "image_size": "2K"}
  }'
```

Per-model reference image caps (verified 2026-09-10):

| Model | Object images | Character images | Style images |
|---|---|---|---|
| `gemini-3.1-flash-lite-image` | up to 14 | N/A | N/A |
| `gemini-3.1-flash-image` | up to 10 | up to 4 | N/A |
| `gemini-3-pro-image` | up to 6 | up to 5 | up to 3 |

### `steps[]` parsing (interleaved text + images)

For stories with multiple illustrations, `output_image` returns only the
*last* image. Walk `steps[]` for the full timeline:

```text
interaction.steps[]:
  - type: "model_output"
    content[]:
      - type: "text",  text: "..."
      - type: "image", data: "<base64>", mime_type: "image/png"
```

Filter `step.type == "model_output"`, then dispatch on `block.type`
(`"text"` vs `"image"`).

### Grounding with Google Search

```bash
curl -s -X POST ".../v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-image",
    "input": "Create a wallpaper of a resplendent quetzal bird using accurate images from search.",
    "tools": [{"type": "google_search"}]
  }'
```

- `gemini-3.1-flash-image` also supports Google **Image** Search grounding.
- `gemini-3.1-flash-lite-image` does **not** support Google Search grounding.

### SynthID

All generated images include a SynthID watermark. There is no opt-out.

## Positive case — generateContent (legacy / agent runtimes)

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-image:generateContent" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "role": "user",
      "parts": [{"text": "A watercolor otter holding a stethoscope"}]
    }],
    "generationConfig": {
      "responseModalities": ["TEXT", "IMAGE"]
    }
  }'
```

Decode image bytes from model parts:
`inlineData.data` (base64) + `inlineData.mimeType`.

## Models (snapshot 2026-09-10)

| Branding | Model id | Notes |
|---|---|---|
| Nano Banana 2 | `gemini-3.1-flash-image` | Default workhorse |
| Nano Banana 2 Lite | `gemini-3.1-flash-lite-image` | Fast/cheap; weak multi-ref / multi-turn edit |
| Nano Banana Pro | `gemini-3-pro-image` | Highest quality / brand control |
| Nano Banana (legacy) | `gemini-2.5-flash-image` | Prefer migrate to 3.1 Flash / Lite |

Optional `generationConfig.imageConfig` (aspect / size) — OpenClaw maps
request sizes into this object when present.

## Editing

Send prior image as `inlineData` (or `fileData`) **plus** a text instruction
in the same user turn. Multi-turn edits must replay **thought signatures**
on prior model parts ([thinking.md](thinking.md)) — validation is strict.

## Runtime routing notes

| Runtime | Path |
|---|---|
| OpenClaw | `extensions/google` → `:generateContent` + `responseModalities` |
| Hermes | Does **not** call AI Studio Imagen directly for the image tool — uses **FAL** `fal-ai/nano-banana-pro` and/or OpenRouter `google/gemini-*-image` |
| Fal plugin (OpenClaw) | Separate Fal-hosted Nano Banana ids — not the google plugin |

## Workflow

1. Pick image model id; confirm key can call it.
2. Generate → decode `inlineData` → write file with matching extension.
3. For edits, include source image + prompt; preserve signatures across turns.
4. Track text+image token usage in `usageMetadata`.

## Edge cases

- Missing thought signatures on edit turns → 400.
- Interleaved text+image stories need full part iteration — convenience
  “last image only” helpers drop earlier images.
- Do not send OpenAI `b64_json` / multipart `/images/edits` shapes here.

## Error handling

400 = modality/config/signature; 429 = quota — [errors.md](errors.md).
