# Provider matrix — OpenAI vs OpenRouter vs Gemini vs Anthropic

Read this when choosing a provider or porting code between them.

**Scope:** public **OpenAI** (`api.openai.com`), **OpenRouter**, Google
**Gemini** native Generative Language API (`generativelanguage.googleapis.com`),
and **Anthropic** native Messages API (`api.anthropic.com`). ChatGPT backend
(`chatgpt.com/backend-api`) is a fifth surface —
see `../codex/chatgpt-backend.md`. Never mix its contract with these columns.

More surfaces — **Bedrock**, **Azure**, **Copilot**, the OpenAI-compatible
long tail, local runtimes, and gateways — are compared in the section below
the table.

| Capability | OpenAI | OpenRouter | Gemini (native) | Anthropic |
|---|---|---|---|---|
| Primary chat API | Responses (`/v1/responses`); legacy Chat Completions | OpenAI-shaped Chat Completions | `models/{id}:generateContent` (+ stream SSE) | Messages: `POST /v1/messages` |
| Auth headers | `Authorization: Bearer` | `Authorization: Bearer` | **`x-goog-api-key`** (Bearer = Vertex/OAuth) | `x-api-key` (or Bearer — never both) + required `anthropic-version` |
| Env vars | `OPENAI_API_KEY` | `OPENROUTER_API_KEY` | `GEMINI_API_KEY` / `GOOGLE_API_KEY` | `ANTHROPIC_API_KEY` |
| System prompt | `instructions` / system message | system message | `systemInstruction` | Top-level `system` (canonical) + mid-conversation `role: "system"` on Fable/Mythos 5.x, Opus 5/4.8 |
| Output cap | `max_output_tokens` optional | OpenAI-style | set `maxOutputTokens` explicitly | **`max_tokens` required** (0 = cache pre-warm) |
| Chat video input | **Not supported** | `video_url` parts (provider-dependent) | `inlineData` / `fileData` video (limits apply) | Not supported (image/PDF input only) |
| Model list API | Minimal name list | Rich pricing/modalities/endpoints | `GET /v1beta/models` with methods + limits | `GET /v1/models` with capabilities + cursor pagination |
| Provider routing / fallbacks | Single vendor | `models[]` + `provider` prefs | Single Google surface (or Vertex) | Single vendor (Bedrock/Vertex/Foundry are separate platforms) |
| Rate limits | Tier TPM/RPM; `x-ratelimit-*` | Per-key dynamic; `:free` caps | RPM/TPM/RPD; `Retry-After` can be hours | Tiered RPM/TPM + spend caps; spend-cap 429 has **no** `retry-after` |
| Streaming | SSE; Chat Completions `[DONE]`; Responses typed events | OpenAI-like SSE + usage quirk | SSE `?alt=sse`; **no** `[DONE]` | Named SSE events, **no** `[DONE]`, incremental deltas |
| Tools | OpenAI function / Responses tools | OpenAI-shaped tools | `functionDeclarations` + `functionCall`/`functionResponse`; schema **subset** | `tools[]`/`tool_choice`; `tool_use` → `tool_result`; `strict` schemas |
| Thinking / reasoning | Model-specific; Responses reasoning | Provider-dependent | `thinkingConfig` — 2.5=`thinkingBudget`, 3=`thinkingLevel`; **thought signatures** | `thinking` blocks + opaque `signature`; adaptive + `effort`; replay unmodified |
| Prompt caching | Automatic prefix; `prompt_cache_key` | Provider-dependent caching | Implicit/explicit caching (2.5 vs 3 differ) | `cache_control` breakpoints; 5m/1h TTL; min-token floors |
| Image generation | `/v1/images/*` GPT Image | `POST /api/v1/images` | generateContent + `responseModalities` (Nano Banana) | — |
| TTS | `/v1/audio/speech` raw bytes | OpenAI-compatible speech | generateContent + `responseModalities: ["AUDIO"]` | — |
| STT | multipart `/v1/audio/transcriptions` | JSON+base64 STT | multimodal input / Live | — |
| Embeddings | `/v1/embeddings` | Routed OpenAI-shaped | `:embedContent` / `:batchEmbedContents` | — |
| Realtime voice | OpenAI Realtime | Not first-class | Gemini Live WebSocket | — |
| OpenAI-compat shim | N/A (is the shape) | Is the product shape | Optional `/v1beta/openai/` — avoid for agents | None (native only) |
| Cloud platforms | N/A | N/A | `../gemini/vertex.md` | Bedrock / Google Cloud / Foundry / AWS platform pages |

## Other surfaces (transports, tails & wrappers)

The table above compares the four **provider APIs**. These surfaces carry the
same models over different wires — read the linked file before porting code:

| Surface | Read | One-line differentiator |
|---|---|---|
| **AWS Bedrock** | `../bedrock/README.md` | SigV4 or `AWS_BEARER_TOKEN_BEDROCK`; model id in the URL path; Converse / ConverseStream; **binary event-stream, not SSE** |
| **Azure OpenAI / Foundry** | `../azure/README.md` | `api-key` or Entra Bearer + required `api-version`; deployment ≠ model; content filtering always on |
| **GitHub Copilot backend** | `../copilot/README.md` | Unofficial proxy; Copilot-token auth; transport chosen by model id (Claude `/v1/messages`, Gemini `/chat/completions`, GPT-5.x `/responses`) |
| **OpenAI-compatible long tail** | `openai-compatible-providers.md` | Groq, DeepSeek, xAI, Mistral, Qwen, Kimi, MiniMax, Together, Fireworks, … — delta table over the OpenAI baseline |
| **Local / self-hosted** | `local-inference.md` | Ollama, llama.cpp, vLLM, LM Studio, SGLang — operational deltas only; no auth; you own the lifecycle |
| **Gateways / proxies** | `gateways.md` | LiteLLM proxy, Cloudflare AI Gateway, Vercel AI Gateway — unified billing/keys/cache/fallback; verify native-feature passthrough |

## Choosing

- Need guaranteed OpenAI ecosystem (Batch, Realtime GA, strict JSON schema) → **OpenAI**.
- Need cost routing / many vendors / `:free` experiments → **OpenRouter**.
- Need Google models, Live, Nano Banana image, or Gemini grounding search → **Gemini native**.
- Need Claude (adaptive thinking, effort, signatures, prompt caching) → **Anthropic native** (`../anthropic/`).
- Need Gemini **via** OpenRouter only for billing consolidation → OpenRouter
  `google/…` ids + OpenRouter refs (not Gemini auth/base URL). Same idea for
  Claude ids on OpenRouter — route to `../openrouter/` then.
- Need AWS IAM / region control / multi-model catalog → **Bedrock** Converse (`../bedrock/`).
- Locked into Azure compliance / RBAC / data residency → **Azure OpenAI** (`../azure/`).
- Private data / offline / zero per-token cost → **local runtimes** (`local-inference.md`).
- One key + budgets + caching across many vendors → **gateway** (`gateways.md`).

## Porting

- OpenAI chat → Gemini: remap `messages`→`contents`, `system`→
  `systemInstruction`, tools→`functionDeclarations`, set `maxOutputTokens`,
  add thought-signature replay, switch auth header.
- OpenAI chat → Anthropic: `messages` stays, but system moves to the top-level
  `system` param; add required `max_tokens` + `anthropic-version`; map
  `tool_calls`→`tool_use`/`tool_result`; drop non-default `temperature`/`top_p`
  on current models; treat `signature` blocks as opaque and replay unchanged.
- Anthropic ↔ Gemini: both replay reasoning signatures, but the wires differ
  (`signature` vs `thoughtSignature`) — never interop values across providers.
- Any → any: re-check the streaming terminator (`[DONE]` or none), usage field
  names, and error envelopes before shipping.

## Related

- Anthropic router: `../anthropic/README.md`
- Gemini router: `../gemini/README.md`
- OpenRouter router: `../openrouter/README.md`
- OpenAI index: `../openai/README.md`
- OpenAI multimodal router: `../openai/multimodal.md`
- Bedrock router: `../bedrock/README.md`
- Azure router: `../azure/README.md`
- Copilot backend: `../copilot/README.md`
- Compat long tail: `openai-compatible-providers.md`
- Local runtimes: `local-inference.md`
- Gateways: `gateways.md`

Shared API techniques:

- Capability selection: `capability-contract.md`
- Context and compaction: `context-management.md`
- MCP protocol/tool boundary: `mcp.md`
- Lifecycle hooks and callbacks: `hooks.md`
- Boundary and scenario verification: `verification.md`
