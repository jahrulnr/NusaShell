# OpenRouter — Speech-to-text (STT)

`POST /api/v1/audio/transcriptions` — JSON in, JSON out.

**Not OpenAI-compatible.** Body is JSON with base64 under
`input_audio: { data, format }` — **not** `multipart/form-data` with a `file`
field. Do not point an OpenAI SDK transcription call at OpenRouter; it sends
the wrong shape. Use curl / fetch / requests directly.

Auth: `OPENROUTER_API_KEY` — [openrouter.ai/keys](https://openrouter.ai/keys).

Upstream: [openrouter-stt](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-stt).
Docs: [STT guide](https://openrouter.ai/docs/guides/overview/multimodal/stt).

## Discover STT models

```bash
curl -sS 'https://openrouter.ai/api/v1/models?output_modalities=transcription' \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  | jq '.data[] | {id, name, pricing}'
```

Use full slugs (`google/chirp-3`, `openai/whisper-1`, `openai/whisper-large-v3`),
not short names.

## Positive case

```bash
# Prefer --data-binary @file so huge base64 stays off argv (ARG_MAX).
MODEL=google/chirp-3
FORMAT=wav   # wav|mp3|flac|m4a|ogg|webm|aac
AUDIO=audio.wav
PAYLOAD=$(mktemp)
jq -n --arg model "$MODEL" --arg data "$(base64 < "$AUDIO" | tr -d '\n')" --arg fmt "$FORMAT" \
  '{model:$model, input_audio:{data:$data, format:$fmt}}' > "$PAYLOAD"

curl -sS -X POST https://openrouter.ai/api/v1/audio/transcriptions \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  --data-binary @"$PAYLOAD"
```

### Response (duration-priced)

```json
{
  "text": "I used to rule the world.",
  "usage": { "seconds": 20, "cost": 0.005333 }
}
```

### Response (token-priced)

```json
{
  "text": "Hello, this is a test of speech-to-text transcription.",
  "usage": {
    "total_tokens": 113,
    "input_tokens": 83,
    "output_tokens": 30,
    "cost": 0.000508
  }
}
```

`usage.cost` is always present. Providers report either `seconds` **or** a
token breakdown — do not assume both.

## Request contract

| Field | Required | Notes |
|---|---|---|
| `model` | yes | Full slug from transcription models list |
| `input_audio.data` | yes | Base64 **raw bytes only** — no `data:audio/...;base64,` prefix |
| `input_audio.format` | yes | `wav` `mp3` `flac` `m4a` `ogg` `webm` `aac` — must match bytes |
| `language` | no | ISO-639-1 (`en`, `ja`); auto-detect if omitted |
| `temperature` | no | 0–1 |
| `provider` | no | Passthrough — see below |

### Format tips

- `wav` / `flac` — quality; large (base64 adds ~33%).
- `mp3` / `m4a` / `aac` — smaller uploads.
- `webm` / `ogg` — typical browser `MediaRecorder`.
- Confirm container with `ffprobe` if garbled.

### Provider passthrough

```json
{
  "model": "openai/whisper-large-v3",
  "input_audio": { "data": "UklGRiQA...", "format": "wav" },
  "provider": {
    "options": {
      "groq": {
        "prompt": "Expected vocabulary: OpenRouter, API, transcription"
      }
    }
  }
}
```

Keys under `provider.options.<slug>` forward only when that provider handles
the request.

## Edge cases / troubleshooting

| Symptom | Cause / fix |
|---|---|
| Garbled / empty `text` | `format` ≠ bytes, or silent audio — `ffprobe` |
| 400 Invalid base64 | Strip data-URI prefix |
| 400 ZodError | Missing/wrong types; nested `message` names path (`input_audio.data` / `.format`) |
| 413 | Payload too large — compress / trim / lower sample rate |
| Model not found | Need full permaslug from models filter |

Contrast OpenAI multipart STT: `../openai/stt.md`.

## Errors

Non-200 → read JSON error body. Shared credit/rate codes: [errors.md](errors.md).

## Related

- [model-list.md](model-list.md) — discovery
- [tts.md](tts.md) — speech out (different shape)
- [generations.md](generations.md) — if a generation id is returned
- [README.md](README.md)
- [Models filter](https://openrouter.ai/models?output_modalities=transcription)
