# OpenAI guides

Language-agnostic HTTP guidance for the public OpenAI platform
(`api.openai.com`).

**This is the API-key surface.** The ChatGPT/Codex backend
(`chatgpt.com/backend-api`) is a **separate product** with its own auth —
see `../codex/`. Never mix the two in one client path.

Auth: `Authorization: Bearer $OPENAI_API_KEY`
(+ optional `OpenAI-Organization`, `OpenAI-Project`).
New builds → **Responses API**; Chat Completions only for legacy/ports.

Read **only** the file for the API you need (split-read rule).

## Router

| Need | Read |
|---|---|
| Responses API (new work) | [responses.md](responses.md) |
| Legacy Chat Completions | [chat-completions.md](chat-completions.md) |
| SSE streaming (both APIs) | [stream.md](stream.md) |
| Function calling / tools | [tools.md](tools.md) |
| Chat multimodal **input** (image/audio/file; no video) | [multimodal.md](multimodal.md) (router + cheat-sheet) |
| Image generation / editing | [images.md](images.md) |
| Speech → text (file STT) | [stt.md](stt.md) |
| Text → speech | [tts.md](tts.md) |
| Video (Sora — deprecated; scheduled 2026-09-24 shutdown) | [video.md](video.md) |
| Realtime voice / live STT / translate | [realtime.md](realtime.md) |
| Embeddings / RAG vectors | [embeddings.md](embeddings.md) |
| Model discovery + naming/prefix map | [model-list.md](model-list.md) |
| Errors / retries / tiers | [errors.md](errors.md) |

This README indexes the tree; [multimodal.md](multimodal.md) owns chat-input
modality rules and the classification cheat-sheet — read it before picking a
dedicated API for a media task.

Optional implementation cross-check: `../_shared/sample-implementations.md`
(LiteLLM `llms/openai/`).

Parent skill routing: `../../SKILL.md`.
OpenRouter / Gemini / Anthropic counterparts: `../openrouter/`, `../gemini/`, `../anthropic/`.
Codex (ChatGPT token) surface: `../codex/`.
Provider matrix: `../_shared/provider-matrix.md`.
