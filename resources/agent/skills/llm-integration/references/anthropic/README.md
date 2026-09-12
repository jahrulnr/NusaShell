# Anthropic / Claude guides

Language-agnostic HTTP guidance for Anthropic's **native Messages API**
(`https://api.anthropic.com`). This is Claude's own API shape — do **not** port
OpenAI request/response conventions into it (different auth, different
`max_tokens` semantics, `thinking` signatures instead of reasoning items).

## Auth + required headers

| Header | Value | Required |
|---|---|---|
| `x-api-key` | Console API key (`sk-ant-api03-…`) | Yes — unless `Authorization` is set |
| `Authorization` | `Bearer <token>` (OAuth / workload-identity tokens) | Alternative to `x-api-key` — **never send both** |
| `anthropic-version` | `2023-06-01` (current and only version) | **Yes, every request** |
| `content-type` | `application/json` | Yes |
| `anthropic-workspace-id` | `wrkspc_…` | Required when the key is multi-workspace |
| `anthropic-beta` | e.g. `thinking-display-updates-2026-08-18` | Only for beta features |

Response headers worth logging: `request-id` (support/debug), `anthropic-organization-id`.
Read **only** the file for the API you need.

## Router

| Need | Read |
|---|---|
| Messages request/response contract | [messages.md](messages.md) |
| SSE streaming events + error recovery | [stream.md](stream.md) |
| Client + server tools (`tool_use` / `tool_result`) | [tools.md](tools.md) |
| Thinking, effort, signatures, block preservation | [thinking.md](thinking.md) |
| Prompt caching (`cache_control`) | [prompt-caching.md](prompt-caching.md) |
| Model discovery / naming / pricing snapshot | [model-list.md](model-list.md) |
| Errors / retries / request IDs | [errors.md](errors.md) |

Parent skill routing: `../../SKILL.md`.
OpenAI / OpenRouter / Gemini counterparts: `../openai/`, `../openrouter/`, `../gemini/`.
Provider matrix: `../_shared/provider-matrix.md`.
Optional implementation cross-check: `../_shared/sample-implementations.md`
(LiteLLM `llms/anthropic/chat/`).

## Anthropic ≠ OpenAI — the diffs that break ports

| Concern | Anthropic | OpenAI |
|---|---|---|
| Auth + versioning | `x-api-key` **or** Bearer + required `anthropic-version` | `Authorization: Bearer` only |
| System prompt | Top-level `system` param (default); mid-conversation `role: "system"` turns on Fable/Mythos 5.x, Opus 5/4.8 | `instructions` / system message |
| `max_tokens` | **Required**; `0` = cache pre-warm; per-model ceiling (64k–128k) | Optional `max_output_tokens` |
| Tools | `tools[]` + `tool_choice`; model emits `tool_use`, you reply `tool_result` | `tools[]` + `tool_calls`/`tool_outputs` |
| Reasoning | `thinking` blocks + opaque `signature` — pass back **unmodified** | reasoning items / summaries |
| Caching | Explicit `cache_control` breakpoints (auto mode available) | Automatic prefix caching |
| Streaming | Named SSE events, no `[DONE]`; deltas are incremental (not cumulative text) | `[DONE]` sentinel (Chat), typed events (Responses) |
| Realtime/TTS/STT/embeddings | **Not on this API** — no first-party equivalents here | Dedicated endpoints |

## Cloud platforms

Claude is also served via Amazon Bedrock, Google Cloud, Microsoft Foundry, and
Claude Platform on AWS — auth + host change (IAM/SigV4), body contract stays the
same. Platform-specific request-size and feature limits apply; keep them out of
this tree and check the platform's docs when targeting one.

## Runtime notes (Hermes / OpenClaw)

| Runtime | Where | Notes |
|---|---|---|
| Hermes | `agent/transports/anthropic.py`, `plugins/model-providers/anthropic` | `sk-ant-api*` keys → `x-api-key`; OAuth tokens → `Authorization: Bearer`; never mixes both |
| OpenClaw | `extensions/anthropic` (+ `anthropic-vertex`, Bedrock paths) | Same dual-auth branch; accumulates `anthropic-beta` flags (e.g. `compact-2026-01-12`) |

Both runtimes send `anthropic-version: 2023-06-01`. Gemini-style tree note:
this tree documents the **HTTP API** — client lowerers live elsewhere.
