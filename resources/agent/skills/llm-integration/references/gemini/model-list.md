# Gemini — Models & discovery

Snapshot date: **2026-09-10**. Always re-verify against live list before
shipping — Google retires preview ids often.

## Discovery

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000" \
  -H "x-goog-api-key: $GEMINI_API_KEY"
```

Each model entry includes `name` (`models/…`), methods
(`generateContent`, `embedContent`, …), token limits, and supported
generation methods. OpenClaw refreshes its google catalog from this API when
keyed; Hermes keeps a curated picker plus models.dev metadata.

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

## Side-surface models (pointers)

| Surface | Example ids | Ref |
|---|---|---|
| Image (Nano Banana) | `gemini-3.1-flash-image`, `gemini-3.1-flash-lite-image`, `gemini-3-pro-image`, legacy `gemini-2.5-flash-image` | [images.md](images.md) |
| TTS | `gemini-3.1-flash-tts-preview` (Hermes may still default older `gemini-2.5-flash-preview-tts`) | [tts.md](tts.md) |
| Embeddings | `gemini-embedding-2`, `gemini-embedding-001` | [embeddings.md](embeddings.md) |
| Live | `gemini-3.1-flash-live-preview` (verify live) | [realtime.md](realtime.md) |

## Selection guidance

- Agent / tools → current Flash with thinking `low` unless task needs Pro.
- Pin explicit preview ids in production; use `*-latest` only in dev.
- After 404 on a preview id, re-run discovery — do not hardcode forever.
- Via OpenRouter → use `google/…` ids and OpenRouter refs, not this tree’s
  auth/base URL.

## Error handling

404 model → discovery + pick replacement. Quota → [errors.md](errors.md).
