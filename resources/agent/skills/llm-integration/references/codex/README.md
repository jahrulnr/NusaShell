# Codex / ChatGPT backend compatibility guides

Language-agnostic HTTP guidance for the ChatGPT Codex backend, derived from
upstream [openai/codex](https://github.com/openai/codex) (`codex-rs/` on `main`).

**This is a separate, compatibility-oriented surface from public
`api.openai.com`.** ChatGPT-token auth +
`https://chatgpt.com/backend-api/codex` must not be mixed with platform API-key
contracts in `../openai/` — see [chatgpt-backend.md](chatgpt-backend.md).

Use this tree only when the user explicitly asks for direct ChatGPT/Codex
backend compatibility or debugging. It is derived from the upstream client and
its backend contract may change; it is not a substitute for the public OpenAI
API or supported Codex product documentation. Never ask the user to paste
access tokens, refresh tokens, or cookies.

Read **only** the file for the API you need.

## Router

| Need | Read |
|---|---|
| OAuth PKCE / device code / auth.json / headers / base URL | [chatgpt-backend.md](chatgpt-backend.md) |
| Responses (SSE / WS) + guardian + compaction_trigger v2 | [responses.md](responses.md) |
| Dedicated `POST /responses/compact` | [compact.md](compact.md) |
| `GET /models` | [models.md](models.md) |
| Image generations / edits | [images.md](images.md) |
| `POST /alpha/search` | [search.md](search.md) |
| Memories `trace_summarize` | [memories.md](memories.md) |
| Realtime calls / websocket | [realtime.md](realtime.md) |
| File upload (`/backend-api/files`, not `/codex`) | [files.md](files.md) |
| WHAM accounts / tasks / usage / settings | [wham.md](wham.md) |
| Connectors directory | [connectors.md](connectors.md) |
| Analytics events / turn costs | [analytics.md](analytics.md) |

Parent routing: `../../SKILL.md`. OpenAI public API: `../openai/`.
OpenRouter: `../openrouter/`.
Anthropic: `../anthropic/`.
Optional implementation cross-check: LiteLLM `llms/chatgpt/`
(authenticator + chat/responses) — see `../_shared/sample-implementations.md`.
