# LLM gateways / proxies

> verified 2026-09-11 against LiteLLM snapshot `c79c73f`,
> openclaw `docs/providers/{litellm,cloudflare-ai-gateway,vercel-ai-gateway}.md`,
> and live Cloudflare / Vercel docs. Unverifiable claims labeled inline.

An LLM gateway sits between your client and the upstream provider API, adding a
control plane: unified billing, virtual keys with budgets/rate limits, caching,
fallback/routing, and observability.

| Situation | Gateway? | Reason |
|---|---|---|
| Multi-provider, cost routing, virtual keys | yes | Only layer that unifies these |
| Single provider, simple chatbot, low traffic | no | Direct call is simpler, lower latency |
| Team / multi-tenant with spend limits | yes | Virtual keys + budgets are the core value |
| Need native provider features (Responses `store`, Gemini thought signatures, Claude prompt-cache breakpoints) | maybe no | Gateways normalize to OpenAI shape, may strip native params |
| Centralized compliance / audit logging | yes | One logging config vs per-client instrumentation |

**Interaction with this skill's rules:** The cross-provider rules in SKILL.md
(timeouts, retries, credential handling, and usage capture when available) still
apply at the edge. The gateway does not exempt your client from transport
timeout or retry discipline.

## LiteLLM proxy

Open-source, self-hosted. OpenAI-compatible `/v1/chat/completions` plus
Anthropic-spec `/v1/messages` and a `/anthropic/*` passthrough.
Grounded on LiteLLM's `litellm/proxy/**` source at the pinned snapshot plus
OpenClaw `docs/providers/litellm.md`; use the upstream repository path when the
local clone is unavailable.

### config.yaml basics

```yaml
model_list:
  - model_name: claude-sonnet-4-6          # what clients call
    litellm_params:
      model: anthropic/claude-sonnet-4-6   # upstream provider/model
      api_key: os.environ/ANTHROPIC_API_KEY
  - model_name: "anthropic/*"              # wildcard: any anthropic/ model
    litellm_params: { model: "anthropic/*", api_key: os.environ/ANTHROPIC_API_KEY }
general_settings: { master_key: sk-1234 }   # env: LITELLM_MASTER_KEY
litellm_settings: { drop_params: True, num_retries: 5,
  context_window_fallbacks: [{"gpt-5-mini": ["gpt-5.5"]}] }
```

Source: `proxy/example_config_yaml/{simple_config,load_balancer,oai_misc_config,pass_through_config}.yaml`.

### Auth: master key + virtual keys

| Concept | Env / field | Notes |
|---|---|---|
| Master key | `general_settings.master_key` / `LITELLM_MASTER_KEY` | Admin; can call `/key/generate` |
| Virtual key | `POST /key/generate` | Per-app key with budgets, model access, rate limits |
| Client auth | `Authorization: Bearer <virtual-key>` | Sent to proxy, not upstream |

Create a virtual key: `POST /key/generate` with `key_alias`, `max_budget`,
`budget_duration` (source: `proxy/management_endpoints/key_management_endpoints.py`).

### Budgets / rate limits + fallbacks (per virtual key)

| Field / mechanism | Source / config key | Detail |
|---|---|---|
| `max_budget` / `budget_duration` | `_types.py:1144,1153` | per-key spend cap + reset period |
| `rpm_limit` / `tpm_limit` | `_types.py:1150-1151` | per-key throughput caps |
| `model_max_budget` / `model_rpm_limit` / `model_tpm_limit` | `_types.py:1158-1163` | per-model caps |
| Load balancing | same `model_name`, multiple `litellm_params` | Router picks among deployments |
| Fallbacks | `litellm_settings.fallbacks` | Retry on a different model group on error |
| Context-window fallbacks | `litellm_settings.context_window_fallbacks` | Switch when context window exceeded |
| Wildcard routing | `model_name: "anthropic/*"` | Route any `anthropic/X` to that provider |
| Adaptive router | `model: auto_router/adaptive_router` | Quality/cost-weighted deployment selection |

Source: `litellm/types/router.py`, `router.py:712-796`, `proxy/_types.py`.

### Endpoints

| Endpoint | Path | Status |
|---|---|---|
| Chat completions | `POST /v1/chat/completions` | stable, OpenAI-shaped |
| Anthropic unified | `POST /v1/messages` | beta |
| Anthropic passthrough | `POST /anthropic/v1/messages` | recommended (preserves native headers) |
| Admin: keys / spend / budgets | `/key/generate`, `/key/info`, `/spend/logs`, `/budget/new` | stable |

Source: `proxy/anthropic_endpoints/endpoints.py`, `proxy/pass_through_endpoints/llm_passthrough_endpoints.py`.

### Proxy behavior notes (openclaw docs)

- Default port `http://localhost:4000`. Native-OpenAI-only shaping does not
  apply through a custom base URL: no `service_tier`, no Responses `store`, no
  prompt-cache hints, no reasoning-effort payload shaping. Attribution headers
  (`originator`, `version`, `User-Agent`) only sent to verified native OpenAI endpoints.

## Cloudflare AI Gateway

Managed, edge-deployed. Adds analytics, caching, rate limiting, dynamic routing.
Grounded on openclaw `docs/providers/cloudflare-ai-gateway.md` + live Cloudflare docs.

### URL pattern + auth

```
https://gateway.ai.cloudflare.com/v1/{account_id}/{gateway_id}/{provider}/…
```

`{provider}` selects the upstream API schema (openai, anthropic, workers-ai, …);
the rest follows the provider's native API path. Two-layer auth:
`cf-aig-authorization` authenticates with the Gateway; the provider key
authenticates with the upstream provider.

| Header | Purpose | When |
|---|---|---|
| `Authorization: Bearer <provider-key>` | Upstream provider auth | always |
| `cf-aig-authorization: Bearer <gateway-token>` | Gateway-level auth | only if Gateway authentication enabled |

### Caching / rate limiting / fallbacks

| Knob | Header / field | Detail |
|---|---|---|
| Cache TTL | `cf-aig-cache-ttl: <seconds>` | min 60s, max 1 month |
| Skip cache | `cf-aig-skip-cache: true` | bypass cache, fetch from provider |
| Custom cache key | `cf-aig-cache-key: <key>` | override default; opts request into caching |
| Rate limit | `rate_limiting_interval` / `_limit` / `_technique` | `fixed` or `sliding` window; per-gateway |
| Dynamic routing | JSON config or visual editor | conditional / percentage / rate-budget nodes with fallback outputs; model nodes with `timeout` + `retries` |

Default cache key = SHA-256(provider + endpoint + model + provider auth header + full request body). Exact match only. Cache status: resp header `cf-aig-cache-status: HIT|MISS`.

## Vercel AI Gateway

Managed, multi-provider. OpenAI Chat, Responses, Anthropic Messages, AI SDK compatible.
Grounded on openclaw `docs/providers/vercel-ai-gateway.md` + live Vercel docs.

### Base URL + auth + routing

| API surface | Base URL |
|---|---|
| OpenAI Chat / Responses | `https://ai-gateway.vercel.sh/v1` |
| Anthropic Messages | `https://ai-gateway.vercel.sh` |
| Model catalog | `GET /v1/models` |

Auth: `Authorization: Bearer $AI_GATEWAY_API_KEY` or `$VERCEL_OIDC_TOKEN` (one key for all providers). Model ref prefix selects upstream: `anthropic/claude-opus-4.6` -> Anthropic, `openai/gpt-5.6-sol` -> OpenAI. Fallbacks via `providerOptions.gateway.models` (works across all formats):

```json
{"model":"anthropic/claude-fable-5","providerOptions":{"gateway":{"models":["anthropic/claude-opus-4.8","google/gemini-3.1-pro-preview"]}}}
```

## Comparison

| Gateway | Base URL pattern | Auth | Key features | When to choose |
|---|---|---|---|---|
| LiteLLM proxy | `http://<host>:4000/v1` (self-hosted) | master key + virtual keys | 100+ providers, config.yaml routing, virtual-key budgets, context-window fallbacks, Anthropic passthrough, admin REST | Self-host, full control, on-prem |
| Cloudflare AI Gateway | `https://gateway.ai.cloudflare.com/v1/{acct}/{gw}/{provider}/…` | provider key + optional `cf-aig-authorization` | edge cache, rate limiting, dynamic routing with fallbacks, unified analytics, BYOK | Edge latency, Cloudflare stack, caching-heavy |
| Vercel AI Gateway | `https://ai-gateway.vercel.sh/v1` (OpenAI) / `…/` (Anthropic) | `AI_GATEWAY_API_KEY` or OIDC | 200+ models, provider-prefix routing, `providerOptions.gateway.models` fallbacks, auto-discovered catalog, BYOK | Vercel deployments, multi-provider minimal config |

## Edge cases

| Hazard | Detail | Mitigation |
|---|---|---|
| Double-retry | Gateway retries + client retries multiply request count and cost | Set client retries to 0 for errors the gateway handles, or disable gateway retries and handle all retry client-side |
| Header passthrough | `anthropic-version`, `anthropic-beta` must reach upstream | Verify gateway forwards custom headers; LiteLLM passthrough (`/anthropic/*`) preserves them; unified `/v1/messages` may rewrite |
| Cache keys | Cloudflare hashes full request body + auth header; any body difference = separate entry | Use `cf-aig-cache-key` for semantic grouping; keep auth header stable across cacheable requests |
| Streaming through proxies | Proxies may buffer SSE chunks, breaking idle-timeout logic | Test streaming end-to-end; LiteLLM supports `include_cost_in_streaming_usage: true`; verify chunk cadence |
| Native param stripping | Gateways normalize to OpenAI shape; `service_tier`, Responses `store`, prompt-cache hints, reasoning-effort shaping may be dropped | Use passthrough endpoints (LiteLLM `/anthropic/*`) or direct calls when native params are load-bearing |
| Usage / billing attribution | Gateway may inject `total_tokens` (OpenAI shape) into Anthropic responses | LiteLLM **can** strip `usage.total_tokens` from `/v1/messages` non-streaming responses, but it is **opt-in** — `litellm.strip_anthropic_total_tokens = True` (default off; `anthropic_endpoints/endpoints.py`); verify usage fields match provider spec |
| Idle timeouts | Gateway idle timeout may be shorter than your client's | Align timeouts; a gateway cutting at 30s defeats a client waiting for a reasoning model |
| Budget exhaustion mid-stream | Gateway may kill a streaming request when a per-key budget is hit mid-response | Handle abrupt stream termination; check budget before long agentic loops |
