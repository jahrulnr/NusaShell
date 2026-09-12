# Gemini — Multimodal input

Use the same [generate-content.md](generate-content.md) endpoint. Attach
media as parts alongside text — there is no separate “vision” API.

## Part shapes

```json
{
  "role": "user",
  "parts": [
    {"text": "Describe this"},
    {
      "inlineData": {
        "mimeType": "image/jpeg",
        "data": "<base64 without data: prefix>"
      }
    }
  ]
}
```

| Part | When |
|---|---|
| `inlineData` | Small/medium payloads (images, short audio, PDFs) as base64 |
| `fileData` | `{ "fileUri": "https://… or files/…", "mimeType": "…" }` after Files API upload / GCS (Vertex) |
| `text` | Always available |

Common MIME types: `image/jpeg`, `image/png`, `image/webp`, `image/gif`,
`application/pdf`, `audio/*`, `video/*` (model- and host-dependent).

## Limits & quirks (from agent runtimes)

- OpenClaw native video understanding: official AI Studio base URL + Gemini
  text model ids; MIME allowlist; ~**20 MB** exclusive request bound on the
  transport path — prefer Files API for large media.
- Hermes maps OpenAI-style `image_url` data-URIs → `inlineData` in the native
  adapter; strip the `data:<mime>;base64,` prefix when building REST yourself.
- PDF / document Q&A uses the same generateContent turn (media understanding),
  not embeddings alone.

## Workflow

1. Confirm the model supports the modality (`GET /models` / docs).
2. Prefer `inlineData` for small assets; Files API for large/repeated.
3. Put instructions in `text` parts in the same user content as the media.
4. For agent loops, keep media parts in history only while needed — they
   dominate tokens.

## Not this file

- **Generating** images → [images.md](images.md)
- **TTS output** → [tts.md](tts.md)
- **Live camera/mic** → [realtime.md](realtime.md)

## Error handling

Unsupported MIME / oversized payload → 400. Fix input; see [errors.md](errors.md).
