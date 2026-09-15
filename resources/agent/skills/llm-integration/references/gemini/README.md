# Gemini / Google AI Studio guides

Language-agnostic HTTP guidance for Google's **native** Generative Language
API (`generativelanguage.googleapis.com`). Grounded in live Google docs plus
how Hermes and OpenClaw wire Gemini in production.

Prefer **native** `generateContent` / `streamGenerateContent` for agent work.
Do **not** default to the `/v1beta/openai` OpenAI-compat shim — see
[openai-compat.md](openai-compat.md).

Auth env (either): `GEMINI_API_KEY` or `GOOGLE_API_KEY`. Header:
`x-goog-api-key` (not `Authorization: Bearer` on the native host).

Read **only** the file for the API you need.

## Router

| Need | Read |
|---|---|
| Chat / generateContent contract | [generate-content.md](generate-content.md) |
| SSE streaming | [stream.md](stream.md) |
| Function calling / tools | [tools.md](tools.md) |
| Thinking / thought signatures | [thinking.md](thinking.md) |
| Model discovery / naming | [model-list.md](model-list.md) |
| Errors / quotas / retries | [errors.md](errors.md) |
| Multimodal **input** (image/audio/video/PDF) | [multimodal.md](multimodal.md) |
| Image **generation** / edit | [images.md](images.md) |
| Text-to-speech | [tts.md](tts.md) |
| Video **generation** (Veo `:predictLongRunning`) | [video.md](video.md) |
| Music **generation** (Lyria, Interactions API) | [music.md](music.md) |
| Embeddings | [embeddings.md](embeddings.md) |
| Live / realtime voice | [realtime.md](realtime.md) |
| Vertex AI differences | [vertex.md](vertex.md) |
| OpenAI-compat `/openai` path | [openai-compat.md](openai-compat.md) |

## API surfaces at a glance

Gemini exposes two request surfaces. Pick one per code path; do not mix.

| Surface | Endpoint | Use for |
|---|---|---|
| `generateContent` / `streamGenerateContent` | `POST /v1beta/models/{id}:generateContent` | Chat, tools, thinking, multimodal **input**, Veo video, legacy image/TTS |
| Interactions API (GA June 2026) | `POST /v1beta/interactions` | New code: image gen/edit, TTS, Lyria music, multi-turn `previous_interaction_id`, background execution |

Veo video is `:predictLongRunning` only (not Interactions). Lyria music is
Interactions only (not `:generateContent`). See [video.md](video.md) and
[music.md](music.md) for the exact contracts.

### Shared root and discovery (verified 2026-09-15)

Both surfaces live on the **same host** (`generativelanguage.googleapis.com`)
and the **same `/v1beta` root**. The Interactions surface does **not** have its
own API root — there is no `/v1beta2/interactions`.

Google's docs disagree with each other and with the live API:

- `https://ai.google.dev/gemini-api/docs/interactions-overview` and the
  image/music/TTS guides use `POST /v1beta/interactions` (correct).
- `https://ai.google.dev/gemini-api/docs/migrate-to-interactions` uses
  `https://generativelanguage.googleapis.com/v1beta2/interactions` in **all
  nine** of its REST snippets. The live API returns **404** for that path —
  do **not** copy the migration guide's `/v1beta2` examples.

Unauthenticated probe (no key needed — a missing key returns **403** when the
path exists and needs auth, **404** when the path/resource does not exist).
Run 2026-09-15 against `https://generativelanguage.googleapis.com`:

| Method + path | Status | Meaning |
|---|---|---|
| `POST /v1beta/interactions` | 403 | exists, needs key |
| `GET /v1beta/interactions` | 403 | collection list exists |
| `GET /v1beta/models` | 403 | model discovery exists |
| `GET /v1beta/files` | 403 | control (known platform endpoint) |
| `POST /v1beta2/interactions` | **404** | does NOT exist |
| `GET /v1beta2/interactions/int_123` | **404** | does NOT exist |
| `GET /v1beta2/models` | 403 | legacy root still serves the models listing (same shared catalog) |
| `GET /v1beta/nonexistent-path` | 404 | control (probe method valid) |

Notes from the same probe:

- Model discovery is a **single shared catalog**: `GET /v1beta/models`. There
  is no Interactions-specific listing. Interactions-only models (e.g. the
  Lyria family) already appear in a keyed `GET /v1beta/models` response.
- `/v1beta2/models` also returns 403 (the legacy root still serves the same
  catalog), but most other `/v1beta2/*` paths are 404 (`/v1beta2/files`,
  `/v1beta2/corpora`, `:generateContent`). Use `/v1beta` as the canonical root.
- Agents (`deep-research-*`, `antigravity-*`) are **not models** and are not
  in the model catalog — they are a separate resource surfaced through the
  Interactions/agents APIs, not via `GET /v1beta/models`.

To re-verify cheaply without a key, replay the probe table above with
`curl -s -o /dev/null -w '%{http_code}' -X <METHOD> <BASE><PATH>`. A 403 means
the path exists and needs auth; a 404 means it does not exist.

Parent skill routing: `../../SKILL.md`.
OpenAI / OpenRouter counterparts: `../openai/`, `../openrouter/`.
OpenCode client wiring (separate tree): `../opencode/`.
Provider matrix: `../shared/provider-matrix.md`.
Optional implementation cross-check: `../shared/sample-implementations.md`
(LiteLLM `llms/gemini/` + Vertex).

## Runtime notes (Hermes / OpenClaw)

| Runtime | Provider id | Native chat | Notes |
|---|---|---|---|
| Hermes | `gemini` (+ aliases `google`, `google-ai-studio`) | Yes (`GeminiNativeClient`) | Free-tier keys blocked at setup; Vertex is separate `vertex` provider |
| OpenClaw | `google` (+ `google-vertex`, optional `google-gemini-cli`) | Yes (`streamGenerateContent`) | Also owns image/TTS/Live/embeddings/search in the google plugin |

How OpenCode lowers Gemini → `../opencode/` (do not load that tree for raw HTTP API work).
Cross-runtime hooks → `../agent-hooks/`.
