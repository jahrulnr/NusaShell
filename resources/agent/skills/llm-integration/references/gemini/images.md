# Gemini — Image generation & editing

Native image models (“Nano Banana” branding) use **generateContent** with
`responseModalities` including `IMAGE` — not OpenAI `/v1/images/*`.

Auth: `x-goog-api-key: $GEMINI_API_KEY`.  
Docs: https://ai.google.dev/gemini-api/docs/image-generation

Google’s newer docs also show an **Interactions API**
(`POST /v1beta/interactions`). Agent runtimes (OpenClaw google plugin) still
call `:generateContent`; both are valid — pick one surface and stick to it.

## Positive case — generateContent

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
