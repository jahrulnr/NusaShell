# Gemini — Music generation (Lyria)

Lyria generates high-fidelity 44.1 kHz stereo audio from text (and, on
`lyria-3.5`, from images). It is exposed **only** through the Interactions
API — there is no `:predictLongRunning` or `:generateContent` path for
music.

Auth: `x-goog-api-key: $GEMINI_API_KEY`.
Docs: https://ai.google.dev/gemini-api/docs/music-generation
Verified 2026-09-10 against the page above (last-updated 2026-09-08 UTC).

Root and discovery are shared with `generateContent`; see [README.md](README.md)
(do not use the `/v1beta2` path from the migration guide).

## Endpoint — Interactions API

```text
POST https://generativelanguage.googleapis.com/v1beta/interactions
```

- Header: `x-goog-api-key: $GEMINI_API_KEY`
- Body: `{"model": "<id>", "input": <string | parts[]>}`
- This is the same `interactions.create` surface used by image/TTS — see
  [images.md](images.md) and [tts.md](tts.md) for the shared contract.

## Models (snapshot 2026-09-10)

| Model | id | Best for | Duration | Output |
|---|---|---|---|---|
| Lyria 3 Clip | `lyria-3-clip-preview` | Short clips, loops, previews | **30s fixed** | MP3 |
| Lyria 3.5 | `lyria-3.5` | Full-length songs (verses, choruses, bridges) | Couple of minutes (prompt-controllable) | MP3 (default) or WAV |
| Lyria 3 Pro Preview | `lyria-3-pro-preview` | Listed in the Interactions model table | — | — |

`lyria-3.5` is the recommended model for full songs. Use `lyria-3-clip-preview`
to iterate on prompts before spending on a full generation. Verify model ids
against https://ai.google.dev/gemini-api/docs/music-generation and the
Interactions model table at
https://ai.google.dev/gemini-api/docs/interactions-overview#supported-models
before shipping.

## Request — text-to-music (clip)

```bash
curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "Content-Type: application/json" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -d '{
    "model": "lyria-3-clip-preview",
    "input": "A short instrumental acoustic guitar piece."
  }'
```

## Request — full-length song

```bash
curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "lyria-3.5",
    "input": "An epic cinematic orchestral piece about a journey home. Starts with a solo piano intro, builds through sweeping strings, and climaxes with a massive wall of sound."
  }'
```

## Request — output format (WAV, `lyria-3.5` only)

```bash
curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "lyria-3.5",
    "input": "A beautiful piano melody.",
    "response_format": {"type": "audio"}
  }'
```

Default output is MP3. `lyria-3.5` can also emit WAV via `response_format`.
The Clip model is MP3 only.

## Request — image-conditioned (`lyria-3.5` only, up to 10 images)

```bash
curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "lyria-3.5",
    "input": [
      {"type": "text", "text": "An atmospheric ambient track inspired by the mood and colors in this image."},
      {"type": "image", "mime_type": "image/jpeg", "data": "<BASE64>"}
    ]
  }'
```

## Response — convenience properties

The `interactions.create` response is an `Interaction` object. For music,
the two convenience properties are:

- `interaction.output_audio` — the last audio content block. `data` is
  base64-encoded audio bytes.
- `interaction.output_text` — the generated lyrics / song structure text.

Decode and write to disk:

```bash
# Extract the last audio block from the response and save it:
curl -s -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"lyria-3-clip-preview","input":"..."}' \
  | jq -r '.steps[] | select(.type=="model_output") | .content[] | select(.type=="audio") | .data' \
  | base64 -d > output.mp3
```

## Response — `steps[]` (full timeline)

For complex/interleaved output (lyrics + audio blocks), convenience
properties drop earlier content. Iterate `steps[]` manually:

```text
interaction.steps[]:
  - type: "model_output"
    content[]:
      - type: "text",  text: "<lyrics or JSON structure>"
      - type: "audio", data: "<base64 mp3/wav>"
```

Filter `step.type == "model_output"`, then walk `content[]` and dispatch on
`block.type` (`"text"` vs `"audio"`). This is the only way to capture every
lyrics block plus every audio block in a single response.

## Prompting

- **Lead with genre** (hip hop, rock, EDM, "Berlin techno", "60s French
  ye-ye pop"). Bespoke/regional variants are attempted but not guaranteed.
- **Specify instruments** when non-default for the genre (e.g. a sax solo
  in a dance track).
- **Song structure** via `[Intro]` → `[Verse 1]` → `[Chorus]` → `[Bridge]`
  → `[Outro]`, or via timestamps `[0:00 - 0:10] Intro: ...`.
- **Custom lyrics** with a `Lyrics:` prefix and section tags. Vocals are
  generated by default; ask for "Instrumental only" to suppress them.
- **Vocal profile** (gender/timbre/range): Female Soprano/Alto, Male
  Tenor/Baritone, Weathered Rocker, etc.
- **Other params**: BPM (`"120 BPM"`), key (`"in G major"`), mood, duration
  (Clip is fixed 30s; `lyria-3.5` duration is prompt-controlled).
- **Language**: lyrics follow the prompt's language. Prompt in French →
  French lyrics.

## Limitations

- **Single-turn.** Multi-turn editing/refining of a generated clip is **not
  supported** in the current Lyria 3.5. Each call is independent.
- **Safety filters** block prompts requesting specific artist voices or
  copyrighted lyrics.
- **SynthID watermark** is embedded in all generated audio; imperceptible
  but present for identification.
- **Determinism.** Results vary between calls even with the same prompt.
- **Length.** Clip = 30s fixed. `lyria-3.5` = "a couple of minutes",
  controllable via prompt/timestamps.
- **No exposed thoughts.** `lyria-3.5` rewrites prompts internally but does
  not expose intermediate "thought" blocks or thought signatures.
- **Interactions-only.** No `:generateContent` / `:predictLongRunning`
  path. Mixing models in a multi-turn Interactions conversation requires
  the next model to accept the previous model's output modality (e.g. a
  text-only model cannot follow a Lyria audio turn).

## Workflow

1. Pick model: `lyria-3-clip-preview` for iteration, `lyria-3.5` for full
   songs.
2. `POST /v1beta/interactions` with `model` + `input` (string or parts[]).
3. For simple use, read `output_audio.data` (base64) → decode → `output.mp3`.
4. For interleaved lyrics+audio, walk `steps[].content[]` and dispatch on
   `block.type`.
5. Optional: `response_format: {"type": "audio"}` on `lyria-3.5` for WAV.

## Edge cases

- **Convenience properties drop content.** `output_audio` returns only the
  *last* audio block; `output_text` returns the *last* text block. Use
  `steps[]` for the full timeline.
- **Image input is `lyria-3.5` only.** Up to 10 images, base64 inline.
- **`store=false` disables `previous_interaction_id`** — but Lyria is
  single-turn anyway, so this rarely matters for music.
- **Do not send `:generateContent` shapes** (`contents`/`parts`) to the
  Interactions endpoint. Use `model` + `input`.

## Error handling

- 400 = bad model id, bad input shape, image on a non-`lyria-3.5` model.
- 429 = quota — see [errors.md](errors.md).
- Safety-blocked prompts return an error response; no audio is generated.

## Related

- Interactions API overview: https://ai.google.dev/gemini-api/docs/interactions-overview
- Image generation (same Interactions surface): [images.md](images.md)
- TTS (same Interactions surface): [tts.md](tts.md)
- Video generation (Veo, `:predictLongRunning` — different surface): [video.md](video.md)
- Errors / quotas / retries: [errors.md](errors.md)
