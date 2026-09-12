# OpenAI — Multimodal router (chat input + dedicated APIs)

Read **only** the file for the API you need. This page is the index + chat
input rules — it is not a substitute for the dedicated contracts.

## Chat / Responses input modalities

Chat Completions and Responses accept **text, image, audio, file** content
parts. There is **no video input** on OpenAI (`video_url` / `input_video` →
400). That extension exists on OpenRouter.

| Part type | Supported | Notes |
|---|---|---|
| `text` | yes | — |
| `image_url` / `input_image` | yes | URL or base64 data URL |
| `input_audio` | yes | `wav`/`mp3` base64 — **audio-capable chat models only** |
| `file` | yes | PDFs/files via base64 or `file_id` (Files API) |
| `video_url` / `input_video` | **no** | 400 — OpenRouter or extract frames client-side |

400 responses that reject a part often list allowed types
(`text`, `image_url`, `input_audio`, `refusal`, `audio`, `file`) — use that
list to see which modality failed.

## Dedicated APIs (split refs)

| Need | Read |
|---|---|
| Image generate / edit | [`images.md`](images.md) |
| File speech → text (STT) | [`stt.md`](stt.md) |
| Text → speech file (TTS) | [`tts.md`](tts.md) |
| Async video generate (Sora — deprecated) | [`video.md`](video.md) |
| Live voice / live STT / translate | [`realtime.md`](realtime.md) |
| Vectors / RAG embeddings | [`embeddings.md`](embeddings.md) |
| HTTP errors / retries / tiers | [`errors.md`](errors.md) |

## Classification cheat-sheet

1. **Understand media inside a chat turn** → content parts above (not
   generation endpoints).
2. **Generate an image/audio/video artifact** → dedicated API file.
3. **Live mic conversation** → `realtime.md` (not request/response STT/TTS).
4. **Video understanding** → OpenRouter or frame extraction — never
   `video_url` on OpenAI chat.
5. **New video generation** → prefer OpenRouter (`../openrouter/video.md`);
   OpenAI Videos/Sora is deprecated with a scheduled 2026-09-24 shutdown;
   verify the official status and do not assume an OpenAI replacement.
