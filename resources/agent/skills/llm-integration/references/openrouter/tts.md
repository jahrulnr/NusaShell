# OpenRouter — Text-to-speech (TTS)

`POST /api/v1/audio/speech` — JSON request, **raw audio bytes** response.

OpenAI-compatible shape: OpenAI SDKs work if `base_url` /
`baseURL` = `https://openrouter.ai/api/v1` + OpenRouter key.

Auth: `OPENROUTER_API_KEY` — [openrouter.ai/keys](https://openrouter.ai/keys).

Upstream: [openrouter-tts](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-tts).
Docs: [TTS guide](https://openrouter.ai/docs/guides/overview/multimodal/tts).

## Discover TTS models and voices

```bash
curl -sS 'https://openrouter.ai/api/v1/models?output_modalities=speech' \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  | jq '.data[] | {id, name, supported_voices, pricing}'
```

Voices are provider-namespaced: OpenAI short names (`alloy`, `nova`); Voxtral
`en_paul_happy`; Kokoro `af_bella`, etc. Do not cross providers.

## Positive case

```bash
curl -sS -X POST https://openrouter.ai/api/v1/audio/speech \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  -D /tmp/tts-headers.txt \
  --output speech.mp3 \
  -d '{
    "model": "openai/gpt-4o-mini-tts-2025-12-15",
    "input": "Hello! This is a text-to-speech test.",
    "voice": "alloy",
    "response_format": "mp3"
  }'
# On non-200, body is JSON error — do not treat as audio.
# Headers: Content-Type, X-Generation-Id (gen-tts-…)
```

Important headers:

| Header | Meaning |
|---|---|
| `Content-Type` | `audio/mpeg` (mp3) or `audio/pcm;rate=…;channels=…` |
| `X-Generation-Id` | `gen-tts-…` — cost/debug via [generations.md](generations.md) |

## Request contract

| Field | Required | Notes |
|---|---|---|
| `model` | yes | Full slug (often dated), e.g. `openai/gpt-4o-mini-tts-2025-12-15` |
| `input` | yes | Text to speak |
| `voice` | yes | Must be in that model's `supported_voices` |
| `response_format` | no | `mp3` or `pcm` — **default is often `pcm`**; set explicitly for playable files |
| `speed` | no | e.g. `1.25` — OpenAI honors; others may ignore/reject |
| `provider` | no | Passthrough |

### Format

- **`mp3`** — compressed, playable; usual choice for files.
- **`pcm`** — raw samples; parse rate/channels from `Content-Type` to wrap WAV;
  for streaming pipelines. Saving pcm as `.mp3` is the top “corrupt audio” bug.

### Provider passthrough (OpenAI instructions)

```json
{
  "model": "openai/gpt-4o-mini-tts-2025-12-15",
  "input": "Welcome to the show.",
  "voice": "alloy",
  "response_format": "mp3",
  "provider": {
    "options": {
      "openai": {
        "instructions": "Speak in a warm, friendly tone with a slow pace."
      }
    }
  }
}
```

## Long inputs

Per-request character limits apply; pricing is typically **per input character**.
Split at sentence/paragraph boundaries; same `model`+`voice`; concatenate with
ffmpeg. Improves time-to-first-audio if streaming chunks.

## Edge cases / troubleshooting

| Symptom | Fix |
|---|---|
| Unplayable / “empty” file | Format/extension mismatch — check `Content-Type` |
| Model does not exist | Use full dated slug from models list |
| ZodError | Required field missing/wrong type (`voice`, …) |
| `speed` no effect | Provider ignores it |

## Errors

Non-200 → JSON error body. See [errors.md](errors.md).

## Related

- [stt.md](stt.md) — transcription (JSON+base64, not this endpoint)
- [generations.md](generations.md) — `X-Generation-Id`
- [model-list.md](model-list.md)
- [README.md](README.md)
- Contrast OpenAI: `../openai/tts.md`
- [Models filter](https://openrouter.ai/models?output_modalities=speech)
