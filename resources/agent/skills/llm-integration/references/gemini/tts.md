# Gemini — Text-to-speech

TTS converts text into single- or multi-speaker audio. It is **controllable**:
natural-language prompts steer style, accent, pace, and tone. Two surfaces
exist:

- **Interactions API** (`POST /v1beta/interactions`) — Generally Available
  since June 2026, recommended for new code, and what current Google samples
  default to.
- **`generateContent`** with `responseModalities: ["AUDIO"]` +
  `speechConfig` — the legacy/agent-runtime path, still fully supported.

TTS output is inline base64 audio (PCM/WAV-related on `generateContent`,
base64 on Interactions) — **not** OpenAI raw `/v1/audio/speech` bytes.

Auth: `x-goog-api-key: $GEMINI_API_KEY`.
Docs (Interactions default): https://ai.google.dev/gemini-api/docs/speech-generation
Interactions overview: https://ai.google.dev/gemini-api/docs/interactions-overview
Verified 2026-09-10 against both pages.

Root and discovery are shared with `generateContent`; see [README.md](README.md)
(do not use the `/v1beta2` path from the migration guide).

## When to use which

- **New code / greenfield** → Interactions API.
- **Matching Hermes / OpenClaw agent runtimes** → `generateContent`.
- TTS models accept **text-only** input and produce **audio-only** output.
  Do not send images/video to a TTS model.

## Positive case — Interactions API (current default)

### Single-speaker

```bash
curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-tts-preview",
    "input": "Say cheerfully: Have a wonderful day!",
    "response_format": {"type": "audio"},
    "generation_config": {
      "speech_config": [
        {"voice": "Kore"}
      ]
    }
  }'
```

- `input` is the transcript string (or typed parts array).
- `response_format: {"type": "audio"}` requests audio output.
- `generation_config.speech_config` is an **array** on Interactions:
  - Single-speaker: `[{"voice": "<name>"}]`
  - Multi-speaker: `[{"speaker": "<name>", "voice": "<name>"}, …]`
- Read audio via `interaction.output_audio.data` (base64). Wrap into a WAV
  container client-side (PCM 24kHz, 16-bit, mono by default).

### Multi-speaker (up to 2 speakers)

```bash
curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-tts-preview",
    "input": "TTS the following conversation between Joe and Jane:\nJoe: How is it going today Jane?\nJane: Not too bad, how about you?",
    "response_format": {"type": "audio"},
    "generation_config": {
      "speech_config": [
        {"speaker": "Joe", "voice": "Kore"},
        {"speaker": "Jane", "voice": "Puck"}
      ]
    }
  }'
```

Speaker names in `speech_config` must match the names used in the prompt
transcript.

### Streaming (3.1+)

Add `"stream": true` and the `Api-Revision: 2026-05-20` header for streamed
audio chunks. Idle timeout still applies; consume promptly.

### `steps[]` parsing

For complex/interleaved output, `output_audio` returns only the *last*
audio block. Walk `steps[]` (same shape as image/music Interactions
responses) and dispatch on `block.type == "audio"`.

## Positive case — generateContent (legacy / agent runtimes)

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

## Models / voices (snapshot 2026-09-10)

| Model | Notes |
|---|---|
| `gemini-3.1-flash-tts-preview` | Current docs default; streaming TTS supported |
| `gemini-2.5-flash-preview-tts` | Still seen as Hermes default — verify against live list |
| `gemini-2.5-pro-preview-tts` | Preview; verify against live list |

TTS exposes **30 prebuilt voices** (verified 2026-09-10). Common pick:
**`Kore`** (Hermes default). Full set (style in italics):

| Voice | Style | Voice | Style | Voice | Style |
|---|---|---|---|---|---|
| Zephyr | *Bright* | Puck | *Upbeat* | Charon | *Informative* |
| Kore | *Firm* | Fenrir | *Excitable* | Leda | *Youthful* |
| Orus | *Firm* | Aoede | *Breezy* | Callirrhoe | *Easy-going* |
| Autonoe | *Bright* | Enceladus | *Breathy* | Iapetus | *Clear* |
| Umbriel | *Easy-going* | Algieba | *Smooth* | Despina | *Smooth* |
| Erinome | *Clear* | Algenib | *Gravelly* | Rasalgethi | *Informative* |
| Laomedeia | *Upbeat* | Achernar | *Soft* | Alnilam | *Firm* |
| Schedar | *Even* | Gacrux | *Mature* | Pulcherrima | *Forward* |
| Achird | *Friendly* | Zubenelgenubi | *Casual* | Vindemiatrix | *Gentle* |
| Sadachbia | *Lively* | Sadaltager | *Knowledgeable* | Sulafat | *Warm* |

TTS auto-detects input language; 60+ BCP-47 codes are supported (en, fr,
de, es, ja, ko, zh-cmn, ar, hi, vi, etc.). Treat both the voice list and
language coverage as snapshots; verify live before shipping.

Multi-speaker: pass an array of `{"speaker": "<name>", "voice": "<id>"}`
in `speech_config` (up to 2 speakers). Speaker names must match the
prompt transcript.

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
