# Copilot — Model catalog + selection

Model discovery, transport routing, and selection for the Copilot API. The
catalog is account- and plan-dependent; always verify against live
`GET /models`.

> Derived from LiteLLM `litellm/llms/github_copilot/` and OpenClaw
> `extensions/github-copilot/{models,model-metadata}.ts`. May change without
> notice. **Derived 2026-09-11.**

## Discovery endpoint

| Field | Value | Source |
|---|---|---|
| Method | `GET` | openclaw `models.ts` |
| Path | `/models` | openclaw, litellm |
| Auth | `Authorization: Bearer <copilot_token>` | openclaw `fetchCopilotModelCatalog` |
| Headers | editor headers + `Copilot-Integration-Id` | openclaw `models.ts` |
| Timeout | 10s (openclaw default) | openclaw `COPILOT_MODELS_LIST_DEFAULT_TIMEOUT_MS` |

## Response shape

`{ "data": [{ "id", "name", "object", "vendor", "preview",
"model_picker_enabled", "model_picker_category", "policy": {"state"},
"capabilities": { "type", "family", "limits": {
"max_context_window_tokens", "max_output_tokens", "max_prompt_tokens" },
"supports": { "vision", "tool_calls", "streaming", "structured_outputs",
"reasoning_effort": [] } } }] }`

Source: openclaw `models.ts` `CopilotApiModelEntry` type. Extra fields are
preserved as `unknown`.

## Filtering rules

| Rule | Source |
|---|---|
| Skip `object != "model"` or `capabilities.type != "chat"` | openclaw `mapCopilotApiModelToDefinition` |
| Skip internal router ids starting with `accounts/` | openclaw |
| Visible: `model_picker_enabled` AND `policy.state` not `disabled`/`unconfigured` | openclaw `isCopilotCatalogModelVisible` |
| Selectable: visible AND `streaming != false` AND `tool_calls == true` | openclaw `isCopilotCatalogModelSelectable` |

## Transport routing by model id

| Model id pattern | Transport | Endpoint | Source |
|---|---|---|---|
| `*claude*` | `anthropic-messages` | `/v1/messages` | openclaw `resolveCopilotTransportApi` |
| `*gemini*` | `openai-completions` | `/chat/completions` | openclaw |
| `*codex*`, `gpt-5.x`, `o-series` | `openai-responses` | `/responses` | openclaw, litellm |
| Everything else | `openai-responses` (default) | `/responses` | openclaw |

Vendor override: if `/models` returns `vendor: "anthropic"`, force
`anthropic-messages` regardless of id (openclaw `resolveCopilotApiForVendor`).

## Model snapshot (static overrides + discovered)

| Model id | Transport | Context | Max output | Reasoning | Source |
|---|---|---|---|---|---|
| `gpt-5.3-codex` | responses | 400k | 128k | yes | openclaw `STATIC_MODEL_OVERRIDES` |
| `gpt-5.4` | responses | 1.05M | 128k | yes | openclaw |
| `gpt-5.5` | responses | 1.05M | 128k | yes | openclaw |
| `claude-opus-4.6-1m` | anthropic-messages | 1M | 64k | yes | openclaw |
| `claude-opus-4.7-1m-internal` | anthropic-messages | 1M | 64k | yes (xhigh) | openclaw |
| `claude-sonnet-5` | anthropic-messages | — | — | — | openclaw `DEFAULT_COPILOT_MODEL` |
| `text-embedding-3-small` | embeddings | — | — | — | openclaw `embeddings.ts` |

This is a snapshot from static fallback catalogs. The live `/models` endpoint
is authoritative and reflects per-account entitlements.

## Reasoning effort support

| Model | Supported efforts | Source |
|---|---|---|
| `gpt-5.5`, `gpt-5.4`, `gpt-5.3-codex` | includes `xhigh` | openclaw `COPILOT_XHIGH_MODEL_IDS` |
| `claude-opus-4.7-1m-internal` | `low`, `medium`, `high`, `xhigh` | openclaw static override |
| `claude-opus-4.6-1m` | `low`, `medium`, `high` | openclaw static override |

Hermes downgrades unsupported efforts to the nearest weaker supported level
(hermes `copilot/__init__.py`).

## Embeddings

Copilot serves embeddings at `POST /embeddings` with standard OpenAI request
shape (`{model, input, ...}`) and response shape. Supported params:
`timeout`, `dimensions`, `encoding_format`, `user` (litellm
`embedding/transformation.py`). Model discovery prefers
`text-embedding-3-small` > `text-embedding-3-large` >
`text-embedding-ada-002` (openclaw `embeddings.ts`).

## Selection

openclaw ranks starter models by: non-preview first, then category
(`versatile` > `lightweight` > `powerful`), then context window, then max
output, then id (openclaw `compareCopilotStarterCandidates`).

## Edge cases

- **Unknown model ids:** openclaw creates synthetic definitions for any
  unrecognized id, defaulting to `openai-responses` transport. The API
  returns its own error if the model is not on the user's plan.
- **Dynamic catalog fallback:** if `/models` fails, fall back to the static
  manifest catalog (openclaw docs). Disable discovery via config
  `discovery.enabled: false`.
- **Model availability depends on GitHub plan and org policy** — always
  verify live.

## Related files

- [README.md](README.md) — router + auth chain
- [chat-completions.md](chat-completions.md) — chat request/response
- [stream.md](stream.md) — SSE streaming
- [errors.md](errors.md) — error handling
