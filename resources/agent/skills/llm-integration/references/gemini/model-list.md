# Gemini — Models & discovery

Snapshot date: **2026-09-15**. Always re-verify against live list before
shipping — Google retires preview ids often.

Source: https://ai.google.dev/gemini-api/docs/models (verified 2026-09-15).

## Discovery

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000" \
  -H "x-goog-api-key: $GEMINI_API_KEY"
```

Each model entry includes `name` (`models/…`), methods
(`generateContent`, `embedContent`, …), token limits, and supported
generation methods. OpenClaw refreshes its google catalog from this API when
keyed; Hermes keeps a curated picker plus models.dev metadata.

Discovery is a **single shared catalog** at `GET /v1beta/models` — there is
no Interactions-specific listing. Interactions-only models (e.g. the Lyria
family) already appear in a keyed `GET /v1beta/models` response. The
`/v1beta2/models` legacy root also serves the same catalog, but the
canonical root is `/v1beta`; the Interactions surface itself has no
`/v1beta2` path (404 live — see [README.md](README.md)). Agents
(`deep-research-*`, `antigravity-*`) are **not models** and are not in this
catalog.

## Naming

| Context | Form | Example |
|---|---|---|
| Native AI Studio | bare id | `gemini-3.6-flash` |
| OpenClaw / OpenRouter style | `google/{id}` | `google/gemini-3.1-pro-preview` |
| Vertex OpenAI-compat | often `google/{id}` | `google/gemini-3.6-flash` |
| Moving aliases | `*-latest` | `gemini-pro-latest`, `gemini-flash-latest` |

Strip `google/` / `gemini/` prefixes before calling native
`:generateContent` unless the Vertex path expects them.

## Text / agent models (snapshot)

Prefer current Flash/Pro from live list. Common ids seen in Hermes/OpenClaw
registries (verify before pin):

| Family | Example ids | Notes |
|---|---|---|
| Gemini 3.x Flash | `gemini-3.6-flash`, `gemini-3-flash-preview`, `gemini-3.1-flash-lite-preview` | Default balance for agents |
| Gemini 3.x Pro | `gemini-3.1-pro-preview`, `gemini-3-pro-preview` | Heavier reasoning; OpenClaw rewrites retired `gemini-3-pro-preview` → `gemini-3.1-pro-preview` |
| Gemini 2.5 | `gemini-2.5-pro`, `gemini-2.5-flash`, `gemini-2.5-flash-lite` | Still listed; use thinkingBudget not thinkingLevel |
| Gemma (eval) | `gemma-4-31b-it`, … | Low free-tier caps; omit thinkingConfig |

Typical context window on current Gemini text models: **1,048,576** input
tokens; max output often **65,536** / **65,535**.

Retired / suppressed in OpenClaw catalog: Gemini **1.5** and many **2.0** /
old 2.5 preview ids — do not start greenfield on them.

## Side-surface models (snapshot 2026-09-15)

Canonical ids from https://ai.google.dev/gemini-api/docs/models. Preview
aliases seen in keyed model lists are noted; re-verify before pinning.

| Surface | Model id | Branding / notes | Ref |
|---|---|---|---|
| Image | `gemini-3.1-flash-image` | Nano Banana 2 (default) | [images.md](images.md) |
| Image | `gemini-3.1-flash-lite-image` | Nano Banana 2 Lite | [images.md](images.md) |
| Image | `gemini-3-pro-image` | Nano Banana Pro | [images.md](images.md) |
| Image | `gemini-2.5-flash-image` | Nano Banana (legacy) | [images.md](images.md) |
| Video | `veo-3.1-generate-preview` | Veo 3.1 (`:predictLongRunning`) | [video.md](video.md) |
| Video | `veo-3.1-lite-generate-preview` | Veo 3.1 Lite | [video.md](video.md) |
| Video | `gemini-omni-1.1-flash` | Gemini Omni Flash (video gen) | [video.md](video.md) |
| Music | `lyria-3.5` | Full-length songs (Interactions only) | [music.md](music.md) |
| Music | `lyria-3-clip-preview` | 30s clips (Interactions only) | [music.md](music.md) |
| Music | `lyria-3-pro-preview` | Previous-gen full songs (Interactions only) | [music.md](music.md) |
| Music | `lyria-realtime-exp` | Realtime music (Interactions only) | [music.md](music.md) |
| TTS | `gemini-3.1-flash-tts-preview` | Current docs default | [tts.md](tts.md) |
| TTS | `gemini-2.5-flash-preview-tts` | Legacy; verify live | [tts.md](tts.md) |
| TTS | `gemini-2.5-pro-preview-tts` | Preview; verify live | [tts.md](tts.md) |
| STT | `gemini-3.5-transcribe` | Speech-to-text (`gemini-3.5-transcribe-live` also listed) | — |
| Embeddings | `gemini-embedding-2` | Current embedding model | [embeddings.md](embeddings.md) |
| Embeddings | `gemini-embedding-001` | Legacy embedding model | [embeddings.md](embeddings.md) |
| Live | `gemini-3.1-flash-live-preview` | Low-latency Live API | [realtime.md](realtime.md) |
| Live | `gemini-2.5-flash-native-audio-preview-12-2025` | Native audio Live (preview) | [realtime.md](realtime.md) |

Preview alias seen in keyed model lists (not on the public docs page as of
2026-09-15; unverified without a key): `nano-banana-pro-preview` — an alias
for the `gemini-3-pro-image` (Nano Banana Pro) family. Record both names
when matching a live catalog; prefer the docs-page id for new code.

## Selection guidance

- Agent / tools → current Flash with thinking `low` unless task needs Pro.
- Pin explicit preview ids in production; use `*-latest` only in dev.
- After 404 on a preview id, re-run discovery — do not hardcode forever.
- Via OpenRouter → use `google/…` ids and OpenRouter refs, not this tree’s
  auth/base URL.

## Error handling

404 model → discovery + pick replacement. Quota → [errors.md](errors.md).
