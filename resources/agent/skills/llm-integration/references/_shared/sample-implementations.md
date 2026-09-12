# Optional implementation cross-checks

This file is an **optional implementation cross-check**, not the API contract
and not SDK setup guidance. Do not load it for an ordinary provider API call;
use it only when implementing a reusable adapter, stream merger, gateway, or
provider translation layer.

## Local runnable sample

For a compact end-to-end public OpenAI example, use
[`scripts/samples/openai-responses/`](../../scripts/samples/openai-responses/).
It is a Go standard-library server with a native browser client and shows
server-side API-key handling, a bounded `getCurrentTime` function-tool loop,
`store: false` history replay, and a stable per-session `prompt_cache_key`.

**LiteLLM** (MIT, BerriAI) is a useful implementation reference: one adapter
per provider for many APIs, converting OpenAI-format ↔ native
request/response, streaming, and errors. The provider references in this skill
and the provider's official documentation remain authoritative.

- Optional local clone: `/tmp/litellm` when present. Do not assume it exists or
  use it as a source of truth; pin an upstream commit before comparing behavior.
- Upstream: <https://github.com/BerriAI/litellm> (paths below say *what* to look
  for and remain useful without a local clone)

## OpenRouter API-native sample catalog

The OpenRouter team maintains a useful per-operation sample pattern. The
catalog below was inspected at commit
[`012c823da319ef10ee899a64aacd10e83ce39c74`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10ee899a64aacd10e83ce39c74).
Use it as an external cross-check, not as a runtime dependency or a license to
copy code verbatim. Prefer the raw API examples in this skill for the contract;
read only the matching upstream skill/script when an operation needs a runnable
reference.

| Operation | Upstream sample | API-native value |
|---|---|---|
| Models | [`openrouter-models`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10ee899a64aacd10e83ce39c74/skills/openrouter-models) — `list-models.ts`, `search-models.ts`, `resolve-model.ts`, `compare-models.ts`, `get-endpoints.ts` | Live model discovery, fuzzy resolution, pricing/capability comparison, provider endpoint health |
| Images | [`openrouter-images`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10ee899a64aacd10e83ce39c74/skills/openrouter-images) — `discover.ts`, `generate.ts`, `edit.ts` | Per-endpoint capability discovery, base64 output decoding, image-to-image input, provider passthrough |
| Speech-to-text | [`openrouter-stt`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10ee899a64aacd10e83ce39c74/skills/openrouter-stt/SKILL.md) | Raw JSON+base64 request, format validation, bounded payload, curl/fetch/requests examples |
| Text-to-speech | [`openrouter-tts`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10ee899a64aacd10e83ce39c74/skills/openrouter-tts/SKILL.md) | Raw audio bytes, content-type/format handling, model/voice discovery, fetch-based output |
| Video | [`openrouter-video`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10e83ce39c74/skills/openrouter-video/SKILL.md) | Submit → poll → download state machine, capability-driven params, webhook signature/idempotency |
| OAuth PKCE | [`openrouter-oauth`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10e83ce39c74/skills/openrouter-oauth/SKILL.md) | Dependency-free browser `fetch`, verifier/challenge, token exchange, explicit storage boundary |
| Generation diagnostics | [`openrouter-generations`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10e83ce39c74/skills/openrouter-generations) — `get-generation.ts`, `get-generation-content.ts` | Cost/latency/provider-chain inspection and optional stored-content retrieval |
| Analytics | [`openrouter-analytics`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10e83ce39c74/skills/openrouter-analytics) — `discover-schema.ts`, `query-analytics.ts`, `suggest-queries.ts` | Management-key separation, live schema discovery, bounded query construction, truncation-aware output |
| Benchmarks | [`openrouter-benchmarks`](https://github.com/OpenRouterTeam/skills/tree/012c823da319ef10e83ce39c74/skills/openrouter-benchmarks) | Citation-preserving rankings plus routable-model availability gate |

## Cookbook — pick the pattern you're building

| You are building | Read (LiteLLM path) | Why it's the sample |
|---|---|---|
| Any provider adapter | `litellm/llms/base_llm/chat/transformation.py` (`BaseConfig`) | The adapter contract: `get_supported_openai_params`, `map_openai_params`, `validate_environment`, `transform_request`, `transform_response`, `get_error_class`, `get_complete_url`. Also `update_optional_params_with_thinking_tokens` — the thinking-budget ⇒ `max_tokens` padding rule |
| Anthropic / Claude client | `litellm/llms/anthropic/chat/transformation.py` (`AnthropicConfig`) | System prompt (`translate_system_message`), tools + tool_choice (`_map_tools`, `_map_tool_choice`, tool-search, MCP), effort (`_map_reasoning_effort`), structured output (`map_response_format_to_anthropic_output_format`), beta headers (`update_headers_with_optional_anthropic_beta`), usage incl. cache fields (`calculate_usage`) |
| Anthropic streaming | `litellm/llms/anthropic/chat/handler.py` (`ModelResponseIterator`) | SSE event → chunk assembly: `chunk_parser`, `_handle_message_delta`, `_handle_redacted_thinking_content`, signature deltas |
| Gemini native | `litellm/llms/gemini/chat/transformation.py` + `litellm/llms/vertex_ai/gemini/vertex_and_google_ai_studio_gemini.py` | history conversion (`_gemini_convert_messages_with_history`), image/file URL→base64 rules, supported-param gating |
| OpenRouter | `litellm/llms/openrouter/chat/transformation.py` | OpenAI-shaped + quirks: `cache_control` relocation, cache flags, streaming handler |
| OpenAI Chat (legacy) | `litellm/llms/openai/chat/{gpt_transformation,gpt_5_transformation,o_series_transformation}.py` | Model-family param gating (o-series rejects `temperature` etc.) |
| OpenAI Responses | `litellm/llms/openai/responses/transformation.py` | Request transform, reasoning-item handling, tool-schema sanitization, terminal-event parsing for streams |
| ChatGPT / Codex backend | `litellm/llms/chatgpt/` (`authenticator.py`, `chat/`, `responses/`) | ChatGPT-token auth + backend request shape |
| AWS Bedrock (Converse + event-stream) | `litellm/llms/bedrock/chat/converse_transformation.py`, `chat/converse_handler.py`, `chat/invoke_handler.py` (`AWSEventStreamDecoder`), `base_aws_llm.py` | Converse transform, URL build, binary event-stream decoding, SigV4/bearer credential chain |
| Azure OpenAI (chat + Responses) | `litellm/llms/azure/chat/gpt_transformation.py`, `azure/responses/transformation.py`, `azure/common_utils.py` | Deployment URL + `api-version`, flattened tools, `status` stripping, Entra/OIDC tokens |
| GitHub Copilot (auth + 3 transports) | `litellm/llms/github_copilot/authenticator.py`, `chat/`, `responses/`, `messages/` | Device flow → token exchange, editor headers, per-transport transforms |
| Long-tail compat vendors | `litellm/llms/<vendor>/chat/transformation.py` (groq, deepseek, mistral, moonshot, dashscope, fireworks_ai, …) | Reasoning-field mapping, thinking toggles, endpoint variants |
| Local runtimes | `litellm/llms/{ollama,hosted_vllm,lm_studio,llamafile}/chat/transformation.py` | Native-vs-compat mapping (`options`/`keep_alive`/`num_ctx`), fake-key fallbacks |
| Gateway / proxy layer | `litellm/proxy/proxy_cli.py`, `proxy/management_endpoints/key_management_endpoints.py`, `proxy/pass_through_endpoints/llm_passthrough_endpoints.py`, `litellm/router.py` | config model_list, virtual keys + budgets, Anthropic passthrough, router fallbacks |
| Stream merge (any provider) | `litellm/litellm_core_utils/streaming_handler.py`, `litellm/litellm_core_utils/streaming_chunk_builder_utils.py` | Chunk accumulation, tool-call JSON assembly, per-provider chunk handlers |
| Error taxonomy | `litellm/exceptions.py` + `litellm/litellm_core_utils/exception_mapping_utils.py` | Unified classes (rate limit, context window, content policy, timeout…) and the raw→unified mapping |
| Retries / fallbacks | `litellm/router.py`, `litellm/litellm_core_utils/fallback_utils.py` | Fallback chains and retry policy in a live system |
| Provider detection & param support | `litellm/litellm_core_utils/get_llm_provider_logic.py`, `litellm/litellm_core_utils/get_supported_openai_params.py` | Model string → provider mapping; which params a model accepts |
| Cost tracking | `litellm/cost_calculator.py` + `model_prices_and_context_window.json` | Per-model pricing/context table used for spend math |
| Reasoning effort mapping | `litellm/litellm_core_utils/reasoning_effort_utils.py` | Effort normalization across vendors |

## Runnable examples (cookbook)

`cookbook/` in the LiteLLM repo holds runnable notebooks/scripts — good
"mini-app" starting points:

- Streaming: `liteLLM_Streaming_Demo.ipynb`, `Claude_(Anthropic)_with_Streaming_liteLLM_Examples.ipynb`
- Tools: `liteLLM_function_calling.ipynb`, `Parallel_function_calling.ipynb`
- Routing / fallback: `litellm_model_fallback.ipynb`, `litellm_router/`
- Cost: `LiteLLM_Completion_Cost.ipynb`
- Per provider: `LiteLLM_OpenRouter.ipynb`, `liteLLM_VertextAI_Example.ipynb`,
  `LiteLLM_Bedrock.ipynb`, `LiteLLM_Azure_and_OpenAI_example.ipynb`
- Adjacent stacks: `veo_video_generation.py`, `nova_sonic_realtime.py`,
  `gollem_go_agent_framework/` (Go agent framework)

## How to use this in a task

1. **Contract first** — read this skill's provider reference (the truth for
   endpoints, headers, params, edge cases).
2. **Sample second (optional)** — if implementing an adapter, read the matching
   LiteLLM file to see how tricky conversion is handled in production (tools,
   thinking/signature replay, cache_control, stream deltas, error mapping).
3. **Port the logic** to the target language — don't vendor files wholesale;
   keep MIT attribution if you copy substantial code.
4. **Verify** with a minimal real request before building the full feature —
   use curl or a raw HTTP client for HTTP, or the native client for
   WebSocket/local/async surfaces.

## Caveats

- LiteLLM moves fast: cite **function names**, not line numbers.
- It's an adapter/proxy — take the transformation logic, leave the platform
  (Router, callbacks, proxy plumbing are litellm-specific).
- Samples are Python; the patterns are portable. The provider references in
  this skill stay language-agnostic and win on any conflict.
- LiteLLM covers provider HTTP APIs only — for OpenCode client lowering or
  agent-hooks runtimes, the `../opencode/` / `../agent-hooks/` trees are their
  own reference samples.
