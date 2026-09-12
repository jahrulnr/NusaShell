# Gemini — Text-to-speech

TTS uses generateContent (or Interactions in newer docs) on a **TTS model**
with `responseModalities: ["AUDIO"]` and `speechConfig` — response audio is
inline base64, not OpenAI raw `/v1/audio/speech` bytes.

Auth: `x-goog-api-key: $GEMINI_API_KEY`.  
Docs: https://ai.google.dev/gemini-api/docs/speech-generation

## Positive case — generateContent (Hermes / OpenClaw shape)

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-tts-preview:generateContent" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "role": "user",
      "parts": [{"text": "Say cheerfully: build something people love."}]
    }],
    "generationConfig": {
      "responseModalities": ["AUDIO"],
      "speechConfig": {
        "voiceConfig": {
          "prebuiltVoiceConfig": {"voiceName": "Kore"}
        }
      }
    }
  }'
```

Decode `candidates[0].content.parts[].inlineData.data` → audio bytes
(MIME from `inlineData.mimeType`, often PCM/WAV-related — convert as needed).

## Models / voices (snapshot)

| Model | Notes |
|---|---|
| `gemini-3.1-flash-tts-preview` | Current docs default; streaming TTS supported on 3.1+ |
| `gemini-2.5-flash-preview-tts` | Still seen as Hermes default — verify against live list |

Common prebuilt voice: **`Kore`** (Hermes default). Docs list additional
prebuilt voices (e.g. Achernar) — treat the set as snapshot; verify live.

Multi-speaker: pass multiple voice configs with `speaker` labels in newer
Interactions examples; stick to single-speaker until you need dialog.

## Runtime notes

| Runtime | Notes |
|---|---|
| Hermes | `tts.provider: gemini` → native generateContent AUDIO modality |
| OpenClaw | google plugin speech provider → same family |
| Live voice agents | Prefer [realtime.md](realtime.md), not one-shot TTS |

## Workflow

1. Select TTS model + voice; curl-proof once.
2. Decode inline audio; do not JSON-parse as “text success”.
3. Cap input length client-side; split long scripts.
4. For streaming playback, use streamGenerateContent / documented 3.1 stream
   path — idle timeout still applies.

## Edge cases

- Using a **text** model with `responseModalities: ["AUDIO"]` → 400.
- OpenAI SDK `audio.speech` against Gemini base URL → wrong contract.
- Newer Google samples use `POST /v1beta/interactions` — fine for greenfield;
  when matching Hermes/OpenClaw, keep generateContent.

## Error handling

[errors.md](errors.md). Empty audio part after 200 → hard failure.
