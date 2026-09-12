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
| Embeddings | [embeddings.md](embeddings.md) |
| Live / realtime voice | [realtime.md](realtime.md) |
| Vertex AI differences | [vertex.md](vertex.md) |
| OpenAI-compat `/openai` path | [openai-compat.md](openai-compat.md) |

Parent skill routing: `../../SKILL.md`.
OpenAI / OpenRouter counterparts: `../openai/`, `../openrouter/`.
OpenCode client wiring (separate tree): `../opencode/`.
Provider matrix: `../_shared/provider-matrix.md`.
Optional implementation cross-check: `../_shared/sample-implementations.md`
(LiteLLM `llms/gemini/` + Vertex).

## Runtime notes (Hermes / OpenClaw)

| Runtime | Provider id | Native chat | Notes |
|---|---|---|---|
| Hermes | `gemini` (+ aliases `google`, `google-ai-studio`) | Yes (`GeminiNativeClient`) | Free-tier keys blocked at setup; Vertex is separate `vertex` provider |
| OpenClaw | `google` (+ `google-vertex`, optional `google-gemini-cli`) | Yes (`streamGenerateContent`) | Also owns image/TTS/Live/embeddings/search in the google plugin |

How OpenCode lowers Gemini → `../opencode/` (do not load that tree for raw HTTP API work).
Cross-runtime hooks → `../agent-hooks/`.
