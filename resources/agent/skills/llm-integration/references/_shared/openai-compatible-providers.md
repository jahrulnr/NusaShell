# OpenAI-compatible provider cluster (long-tail vendors)

A router for vendors that clone the OpenAI Chat Completions shape but do not
merit their own reference tree. **Verified 2026-09-11** against live vendor
docs, LiteLLM source snapshot `c79c73f`, and OpenClaw/Hermes provider plugins.
Model ids are snapshots; re-verify through each vendor's documented discovery
surface rather than assuming `GET /models`.

## What "OpenAI-compatible" means

These vendors accept `POST <base>/chat/completions` with an OpenAI-shaped body
(`model`, `messages`, `tools`, `stream`, `response_format`, ...) and return an
OpenAI-shaped response (`choices[].message`, `usage`, `finish_reason`). Auth is
almost always `Authorization: Bearer <key>`. Streaming is OpenAI SSE with
`data: {...}` chunks and a terminating `data: [DONE]`.

The **baseline contract** they implement is the OpenAI Chat Completions shape
documented in `../openai/chat-completions.md`. Read that file first for the
canonical request/response fields; this file records only the **deltas** (base
URL, auth, model ids, rejected params, reasoning-field name, quirks).

## Promotion rule: when a vendor gets its own tree

Promote a vendor out of this file into `references/<vendor>/` when **any** of:
- It has a **distinct auth model** (OAuth, signed URLs, mTLS) or a non-Bearer
  header that changes the client path (xAI Responses, Gemini `x-goog-api-key`).
- It exposes **modality APIs** with a non-OpenAI contract (Gemini
  `generateContent`, MiniMax `/v1/image_generation`, xAI realtime WS).
- It has **enough quirks** (reasoning replay rules, streaming deviations,
  error envelopes) to fill a full file set per `provider-template.md`.

Gemini, Vertex, OpenAI, OpenRouter, and Anthropic already have their own trees
(`../gemini/`, `../openai/`, `../openrouter/`, `../anthropic/`). This file
covers only vendors **without** one. It is a router, not a contract dump,
when a vendor here grows quirks, promote it.

## Master delta table

| Vendor | Base URL | Auth header | Env var | Notable model ids | Gotchas |
|---|---|---|---|---|---|
| xAI | `https://api.x.ai/v1` | `Bearer` | `XAI_API_KEY` | `grok-4.6`, `grok-4.5`, `grok-4.3`, `grok-build-0.1` | Chat Completions deprecated, prefer Responses; reasoning only via Responses; `presence_penalty`/`frequency_penalty`/`stop` rejected on reasoning models |
| Groq | `https://api.groq.com/openai/v1` | `Bearer` | `GROQ_API_KEY` | `qwen/qwen3.6-27b`, `openai/gpt-oss-120b`, `openai/gpt-oss-20b` | Native reasoning field is `reasoning` (not `reasoning_content`); `reasoning_format` parsed/raw/hidden; `reasoning_effort` low/medium/high |
| DeepSeek | `https://api.deepseek.com` | `Bearer` | `DEEPSEEK_API_KEY` | `deepseek-v4-pro`, `deepseek-v4-flash` | `reasoning_content` native; thinking on by default; must replay `reasoning_content` in tool loops; `deepseek-chat`/`deepseek-reasoner` retired 2026-07-24 |
| Mistral | `https://api.mistral.ai/v1` | `Bearer` | `MISTRAL_API_KEY` | `mistral-large-latest`, `mistral-medium-3-5`, `mistral-small-latest` | `reasoning_effort` none/high on small + medium-3-5 + magistral; rejects `reasoning_effort=high` + `temperature=0`; drops tool `name` field |
| Cerebras | `https://api.cerebras.ai/v1` | `Bearer` | `CEREBRAS_API_KEY` | `gpt-oss-120b`, `gemma-4-31b`, `zai-glm-4.7` | `reasoning_effort`; static 3-model catalog, no live discovery; `zai-glm-4.7` deprecating 2026-08-17 |
| Moonshot/Kimi | `https://api.moonshot.ai/v1` (intl) / `https://api.moonshot.cn/v1` (CN) | `Bearer` | `MOONSHOT_API_KEY` | `kimi-k3`, `kimi-k2.7-code` | K3 `reasoning_effort` low/high/max (always on); K2.7 Code always-on reasoning; bare-string `reasoning_effort` (object form 400s) |
| Qwen/DashScope | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` (intl) / `https://dashscope.aliyuncs.com/compatible-mode/v1` (CN) | `Bearer` | `DASHSCOPE_API_KEY` / `QWEN_API_KEY` | `qwen3.7-plus`, `qwen3.6-plus`, `qwen3.7-max`, `qwen3.6-flash` | Two endpoints (intl vs CN); `qwen3.7-max`/`qwen3.6-flash` Standard-only, not Coding Plan |
| MiniMax | `https://api.minimax.io/v1` (intl) / `https://api.minimaxi.com/v1` (CN) | `Bearer` | `MINIMAX_API_KEY` | `MiniMax-M3`, `MiniMax-M2.7` | Also offers Anthropic-compat `/anthropic`; M2.x leaks `reasoning_content` in OpenAI delta unless thinking disabled; M3 uses `thinking:{type:adaptive}` |
| Together | `https://api.together.xyz/v1` | `Bearer` | `TOGETHER_API_KEY` | `moonshotai/Kimi-K2.6`, `deepseek-ai/DeepSeek-V4-Pro`, `meta-llama/Llama-3.3-70B-Instruct-Turbo` | Model refs are `<org>/<model>`; also async video gen |
| Fireworks | `https://api.fireworks.ai/inference/v1` | `Bearer` | `FIREWORKS_API_KEY` | `accounts/fireworks/routers/glm-5p2-fast`, `accounts/fireworks/models/kimi-k2p6` | `reasoning_effort`; Kimi models leak CoT unless thinking forced off; rejects `thinking` + `reasoning_effort` together |
| NVIDIA NIM | `https://integrate.api.nvidia.com/v1` | `Bearer` | `NVIDIA_API_KEY` | `nvidia/nemotron-3-ultra-550b-a55b` | Free tier; featured-model catalog from `assets.ngc.nvidia.com`; reasoning text exposed |
| DeepInfra | `https://api.deepinfra.com/v1/openai` | `Bearer` | `DEEPINFRA_API_KEY` | `deepseek-ai/DeepSeek-V4-Flash`, `zai-org/GLM-5.2` | `reasoning_effort`; live model discovery; `tool_choice=auto`/`none` only (no specific tool) |
| Novita | `https://api.novita.ai/openai/v1` | `Bearer` | `NOVITA_API_KEY` | `deepseek/deepseek-v4-pro`, `moonshotai/kimi-k3` | Routes are `<org>/<model>`; routes can be added/removed by Novita |
| HuggingFace router | `https://router.huggingface.co/v1` | `Bearer` | `HF_TOKEN` / `HUGGINGFACE_HUB_TOKEN` | `deepseek-ai/DeepSeek-R1`, `Qwen/Qwen3-8B` (dynamic) | Append `:fastest`/`:cheapest` to model id; chat completions only (no image/embed via this router) |
| Baseten | `https://inference.baseten.co/v1` | `Bearer` | `BASETEN_API_KEY` | `thinkingmachines/inkling`, `zai-org/GLM-5.2` | Authenticated discovery (account-scoped model set); per-model deployment URLs also exist |
| Arcee | `https://api.arcee.ai/api/v1` | `Bearer` | `ARCEEAI_API_KEY` | `trinity-large-thinking`, `trinity-large-preview`, `trinity-mini` | Also reachable via OpenRouter; Apache-2.0 models; `trinity-large-thinking` has extended thinking |

Lower-confidence vendors (docs thin or single-source, verify before production):

| Vendor | Base URL | Auth | Env var | Notable ids | Confidence |
|---|---|---|---|---|---|
| Chutes | `https://llm.chutes.ai/v1` | `Bearer` | `CHUTES_API_KEY` | `zai-org/GLM-5.2-TEE`, `deepseek-ai/DeepSeek-V3.2-TEE` | medium, OpenClaw-only cross-check |
| Featherless | `https://api.featherless.ai/v1` | `Bearer` | `FEATHERLESS_API_KEY` | `Qwen/Qwen3-32B`, `moonshotai/Kimi-K2-Instruct` | medium, OpenClaw-only |
| GMI Cloud | `https://api.gmi-serving.com/v1` | `Bearer` | `GMI_API_KEY` | `openai/gpt-5.6-sol`, `anthropic/claude-sonnet-5` | medium, OpenClaw-only |
| Upstage | `https://api.upstage.ai/v1` | `Bearer` | `UPSTAGE_API_KEY` | Solar models (verify live) | low, Hermes plugin only |
| StepFun | `https://api.stepfun.ai/v1` (intl) / `https://api.stepfun.com/v1` (CN) | `Bearer` | `STEPFUN_API_KEY` | `step-3.5-flash`, `step-3.7-flash` | medium, OpenClaw-only |

## Per-vendor notes

The master table is the primary reference; these notes add only what it cannot
capture compactly.

**xAI** — Chat Completions deprecated; prefer Responses (`/v1/responses`) with
`reasoning:{effort:...}`. Reasoning content only via Responses (encrypted),
Chat Completions returns none. `reasoning_effort` defaults `high`, cannot
disable on grok-4.5+. Long context (>200k) doubles rates on grok-4.5/4.6. Docs:
`docs.x.ai`.

**Groq** — Native streaming delta field is **`reasoning`** (not
`reasoning_content`); LiteLLM maps it. `reasoning_format: "parsed"` yields a
dedicated field, `"raw"` wraps in think tags (default), `"hidden"` suppresses.
Structured outputs on a model subset. Docs: `console.groq.com/docs/reasoning`.

**DeepSeek** — Returns **`reasoning_content`** natively beside `content`.
Thinking on by default on v4-pro/v4-flash; toggle via
`{"thinking":{"type":"enabled|disabled"}}` or `reasoning_effort` (`"none"`
disables). **Must replay `reasoning_content`** on follow-up turns in tool loops
or 400. Reasoning tokens count against `max_tokens`. `deepseek-chat`/
`deepseek-reasoner` retired 2026-07-24. Docs: `api-docs.deepseek.com`.

**Mistral** — `reasoning_effort` (none/high) on small-latest, medium-3-5,
magistral. **Rejects `reasoning_effort=high` + `temperature=0`** (400), leave
temperature unset when reasoning on. Drops `name` from tool messages. Also
embeddings (`mistral-embed`) + Voxtral audio. Docs: `docs.mistral.ai/api`.

**Cerebras** — Static 3-model catalog, no `GET /models` discovery.
`zai-glm-4.7` deprecating 2026-08-17. Docs: `inference-docs.cerebras.ai`.

**Moonshot/Kimi** — K3 `reasoning_effort` accepts `low`/`high`/`max` as a **bare
string** (object form 400s), always on. K2.7 Code always-on reasoning. K3 fixes
`temperature`/`top_p`/`frequency_penalty` to provider defaults. Docs:
`platform.kimi.ai/docs`.

**Qwen/DashScope** — Model refs are bare ids (`qwen3.7-plus`), not
`<org>/<model>`. `qwen3.7-max`/`qwen3.6-flash` are Standard-only (not Coding
Plan). Docs: `help.aliyun.com/zh/model-studio`.

**MiniMax** — Also offers Anthropic-compat `/anthropic`. On the OpenAI-compat
path M2.x leaks `reasoning_content` in delta chunks unless thinking disabled; M3
uses `thinking:{type:adaptive}`. Image/video/TTS/music use **separate**
dedicated endpoints. Docs: `platform.minimax.io`.

**Together** — Model refs are `<org>/<model>`. Also async video gen. Docs:
`docs.together.ai`.

**Fireworks** — Model refs are `accounts/fireworks/{routers,models}/<id>`.
**Rejects `thinking` + `reasoning_effort` together**. Kimi models leak CoT
unless thinking forced off. Docs: `docs.fireworks.ai`.

**NVIDIA NIM** — Free tier. Featured catalog from
`assets.ngc.nvidia.com/.../featured-models.json`. Reasoning text exposed in
output. Docs: `build.nvidia.com`.

**DeepInfra** — Live discovery via `GET /v1/openai/models?...`. `tool_choice`
accepts only `auto`/`none`. Also TTS/embeddings/rerank. Docs:
`deepinfra.com/docs/advanced/openai_api`.

**Novita** — Routes are `<org>/<model>`, can be added/removed by Novita. Docs:
`novita.ai/docs`.

**HuggingFace router** — Chat completions only; image/embed/speech need HF
inference clients directly. Append `:fastest`/`:cheapest` to model ids. Docs:
`huggingface.co/docs/inference-providers`.

**Baseten** — Authenticated discovery (account-scoped model set). Per-model
deployment URLs also exist for non-OpenAI-compat calls. Docs: `docs.baseten.co`.

**Arcee** — Trinity MoE, Apache-2.0. Also reachable via OpenRouter. Docs:
`arcee.ai`.

## Reasoning field cheat sheet

| Vendor | Request field | Response field | Notes |
|---|---|---|---|
| xAI | `reasoning:{effort}` (Responses) | encrypted reasoning (Responses only) | Chat Completions returns no reasoning |
| Groq | `reasoning_effort` + `reasoning_format` | `reasoning` (delta) | `parsed` -> `reasoning_content`; default `raw` = think tags |
| DeepSeek | `reasoning_effort` / `thinking:{type}` | `reasoning_content` | Replay in tool loops; counts vs `max_tokens` |
| Mistral | `reasoning_effort` | `reasoning_content` (via thinking blocks) | Rejects `high` + `temperature:0` |
| Cerebras | `reasoning_effort` | (model-dependent) | |
| Moonshot | `reasoning_effort` (bare string) | (model-dependent) | K3 always on; object form 400s |
| Qwen | (model-dependent) | (model-dependent) | Verify per model |
| MiniMax | `thinking:{type}` (Anthropic-compat) | `reasoning_content` (OpenAI delta) | M2.x leaks unless disabled |
| Together | `reasoning_effort` | (model-dependent) | |
| Fireworks | `reasoning_effort` | (model-dependent) | No `thinking` + `reasoning_effort` together |
| NVIDIA | (model-dependent) | reasoning text in output | |
| DeepInfra | `reasoning_effort` | (model-dependent) | |
| Others | (verify live) | (verify live) | Most pass through upstream model behavior |

## Tool-calling and streaming quirks

- All vendors here use OpenAI-shaped `tools`/`tool_choice`/`tool_calls` unless
  noted. DeepInfra restricts `tool_choice` to `auto`/`none`.
- Streaming is OpenAI SSE (`data: {...}` + `data: [DONE]`) on all vendors here.
- DeepSeek requires `reasoning_content` replay between tool turns, or 400.
- Fireworks Kimi models leak CoT unless thinking is forced off.
- MiniMax M2.x leaks `reasoning_content` in OpenAI delta chunks unless thinking
  disabled, use the Anthropic-compat endpoint or disable thinking.
- Mistral drops the `name` field from tool messages.

## Rate-limit headers

Most vendors return standard `x-ratelimit-*` / `retry-after` headers. Treat
them like OpenAI's: honor `Retry-After` on 429 but cap it at the configured
product wait budget. Vendor-specific header names were not uniformly
documented, so verify live if you depend on them.

## Related

- `../_shared/provider-matrix.md` (OpenAI vs OpenRouter vs Gemini vs Anthropic)
- `../_shared/provider-template.md` (how to promote a vendor to its own tree)
- `../_shared/sample-implementations.md` (optional LiteLLM implementation
  cross-check per vendor)
- `../openai/chat-completions.md` (the baseline contract these vendors clone)
- `../openrouter/README.md` (multi-provider router, also reaches these vendors)
- Per-provider trees: `../gemini/`, `../anthropic/`, `../openai/`, `../openrouter/`
