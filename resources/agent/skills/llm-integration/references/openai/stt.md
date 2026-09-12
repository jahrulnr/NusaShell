# OpenAI — Speech-to-text (transcriptions / translations)

File STT is **multipart/form-data** — opposite of OpenRouter's JSON + base64
shape. Do not point an OpenAI SDK at OpenRouter STT (or vice versa) without
adapting the body.

Auth: `Authorization: Bearer $OPENAI_API_KEY`.

Live / microphone streaming → use `realtime.md` (`gpt-live-transcribe`), not
this file.

## Model choice (new work)

| Need | Model / endpoint |
|---|---|
| Default file transcription | `gpt-transcribe` → `POST /v1/audio/transcriptions` |
| Speaker labels (diarization) | `gpt-4o-transcribe-diarize` |
| Word timestamps / `srt` / `vtt` | `whisper-1` |
| Translate recording → English | `whisper-1` → `POST /v1/audio/translations` |
| Live transcript deltas | Realtime: `gpt-live-transcribe` (see `realtime.md`) |

Legacy still works where supported: `gpt-4o-transcribe`,
`gpt-4o-mini-transcribe`. Prefer `gpt-transcribe` for new integrations.

## Positive case — transcription

```bash
curl https://api.openai.com/v1/audio/transcriptions \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -F file=@speech.mp3 \
  -F model=gpt-transcribe \
  -F response_format=json
```

Minimal success response:

```json
{ "text": "Today is a wonderful day to build something people love." }
```

## Request contract — `POST /v1/audio/transcriptions`

`multipart/form-data` fields:

| Field | Required | Notes |
|---|---|---|
| `file` | yes | Audio upload (`flac`, `mp3`, `mp4`, `mpeg`, `mpga`, `m4a`, `ogg`, `wav`, `webm`, …). Filename extension should match the container. |
| `model` | yes | See table above |
| `language` | no | ISO-639-1 single hint — used by whisper / older models |
| `languages` | no | **List** of expected languages — used by `gpt-transcribe` / `gpt-live-transcribe` (do not assume `language` works on every model) |
| `prompt` | no | Free-form context about the recording (topic/setting) — not a restatement of “please transcribe” |
| `keywords` | no | Literal terms that may appear (product names, meds, acronyms) — hints, not forced output |
| `response_format` | no | `json` (default for many models), `text`, `verbose_json`, `srt`, `vtt`, `diarized_json` — **capability is model-scoped** (`srt`/`vtt` → `whisper-1`) |
| `temperature` | no | Sampling; mostly whisper-era |
| `timestamp_granularities[]` | no | `word` / `segment` with `verbose_json` (whisper) |

## Positive case — English translation

```bash
curl https://api.openai.com/v1/audio/translations \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -F file=@spanish.mp3 \
  -F model=whisper-1
```

Always outputs English text regardless of source language.

## Response contract

- Default JSON: `{ "text": "..." }` — treat empty/whitespace `text` as failure.
- `verbose_json` (whisper): adds `language`, `duration`, `segments`, optional
  `words`.
- `diarized_json`: speaker-segment annotations — parse `segments[]` with
  speaker labels/timestamps.
- Streamed file transcription (when enabled for the model): SSE/event stream of
  transcript deltas — still a **file** workflow, not Realtime live audio.

## Workflow

1. Pick file vs live (`realtime.md`) first.
2. Choose model by capability (timestamps / diarize / translate), not by habit.
3. Send multipart with a real filename + matching codec.
4. Parse `text` (or segments) → reject empty transcripts.
5. Long audio: split into chunks under the model's size/duration limit; stitch
   text with overlap or silence-aware cuts.

## Edge cases

- **JSON body / base64** → 400 or silent SDK failure. OpenAI STT is multipart;
  OpenRouter STT is JSON. Mixing them is the #1 integration bug.
- **Wrong extension / codec mismatch** → garbled text or 400. Verify with
  `ffprobe` before blaming the model.
- **`language` vs `languages`**: new `gpt-transcribe` family prefers
  `languages`; older models use singular `language`. Sending the wrong one is
  ignored or 400 depending on model.
- **`srt`/`vtt` on non-whisper models** → 400. Switch to `whisper-1` or drop
  the format.
- **Oversized files** → 413 / 400. Chunk audio; do not retry the same blob.
- **Empty transcript with HTTP 200** (silence, wrong language, corrupt file) —
  fail closed; optionally retry once with a language hint / different model.
- Prompt/keywords are **context**, not instructions to invent speech that is
  not in the audio.

## Error handling

400 = shape/param/model mismatch; 413 = too large; 429 = audio-minute / RPM
limits — see `errors.md`. Never blind-retry a multipart body that failed
validation.
