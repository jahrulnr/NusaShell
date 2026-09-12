# OpenAI — Text-to-speech

`POST /v1/audio/speech` turns text into spoken audio. Response body is **raw
audio bytes** on success; errors are JSON. Auth: `Authorization: Bearer
$OPENAI_API_KEY`.

Live bidirectional voice agents → `realtime.md`, not this file.

## Positive case

```bash
curl https://api.openai.com/v1/audio/speech \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini-tts",
    "input": "Today is a wonderful day to build something people love!",
    "voice": "coral",
    "instructions": "Speak in a cheerful and positive tone.",
    "response_format": "mp3"
  }' \
  --output speech.mp3
```

Success = non-empty binary file. Do **not** `JSON.parse` the 200 body.

## Request contract

```json
{
  "model": "gpt-4o-mini-tts",
  "input": "text to synthesize",
  "voice": "coral",
  "instructions": "optional tone / pacing / accent guidance",
  "response_format": "mp3",
  "speed": 1.0
}
```

| Field | Required | Notes |
|---|---|---|
| `model` | yes | Prefer `gpt-4o-mini-tts`. Also `tts-1` (lower latency), `tts-1-hd` (higher quality, older). |
| `input` | yes | Text to speak. Per-request character limits apply — split long scripts. |
| `voice` | yes | Built-in voice id (model-scoped — see below) |
| `instructions` | no | First-class on `gpt-4o-mini-tts` for accent/emotion/tone/speed/whisper. Ignored or unsupported on older `tts-1*`. |
| `response_format` | no | `mp3` (default), `opus`, `aac`, `flac`, `wav`, `pcm`. Set explicitly when you need playable files. |
| `speed` | no | Typically ~0.25–4.0 on models that honor it |

### Voices (snapshot)

`gpt-4o-mini-tts` family commonly supports: `alloy`, `ash`, `ballad`,
`coral`, `echo`, `fable`, `nova`, `onyx`, `sage`, `shimmer`, `verse`,
`marin`, `cedar`. Docs recommend `marin` / `cedar` for best quality.

`tts-1` / `tts-1-hd` use a smaller set (no `marin`/`cedar`/`ballad`/`verse`
on many snapshots). Realtime voices are a **different** set — see
`realtime.md`.

## Response contract

- **200**: raw audio stream/body. Save with the extension matching
  `response_format` (`.mp3`, `.wav`, `.opus`, …).
- **PCM**: usually 24 kHz mono raw samples — wrap in WAV client-side or play
  with a PCM player. Keep `Content-Type` if present (may carry rate/channels).
- **Streaming**: chunked transfer encoding is supported — play/write as chunks
  arrive; do not buffer the entire response unless required.
- **Non-2xx**: JSON error envelope (`errors.md`). Detect errors by status
  code, not by sniffing the first bytes as JSON on 200.

## Workflow

1. Prefer `gpt-4o-mini-tts` + explicit `response_format` (`mp3` for files,
   `pcm`/`wav` for low-latency playback).
2. Set `voice` + optional `instructions` for product tone.
3. Write raw bytes; verify file size &gt; 0 and playable.
4. Long narration: split on sentence boundaries, keep same `model`+`voice`,
   concatenate audio (priced per character — splitting does not multiply
   model cost beyond the characters themselves).
5. Disclose to end users that speech is AI-generated (policy requirement).

## Edge cases

- **Parsing 200 as JSON** → false “empty/corrupt” bugs. Always branch on HTTP
  status first.
- **Extension mismatch** (`pcm` saved as `.mp3`) → unplayable output that
  looks like a model failure.
- **Unknown voice for model** → 400. Validate voice against the model family.
- **Huge `input`** → 400. Chunk at sentence boundaries; reuse voice/model.
- **`instructions` on `tts-1`** may be ignored — do not rely on them for
  legacy models.
- Empty audio body with 200 is a hard failure — retry once only if the
  failure looks transient (429/5xx), never on 400.
- OpenAI-compatible gateways: TTS is usually SDK-compatible with a base-URL
  swap; STT is often **not** — see OpenRouter `stt.md`.

## Error handling

400 = bad voice/format/input; 401/403 auth; 429 rate limits — see `errors.md`.
Retry only transient failures; never retry a known-bad voice or oversized
input unchanged.
