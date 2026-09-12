# Azure OpenAI - Chat Completions

OpenAI-shaped Chat Completions on the Azure-hosted data plane. Use for legacy
integrations or features not yet on the Responses API. New work -> `responses.md`.

Verified 2026-09-11 (Azure OpenAI REST reference; LiteLLM `llms/azure/chat/`).

## Endpoint + auth

Two URL styles (pick one per client path; never mix):

```bash
# Deployment-based, date-versioned (legacy, still common)
POST https://{resource}.openai.azure.com/openai/deployments/{deployment}/chat/completions?api-version=2024-10-21
api-key: $AZURE_OPENAI_API_KEY

# v1 stable (Foundry Models API) - model in body, like public OpenAI
POST https://{resource}.openai.azure.com/openai/v1/chat/completions?api-version=v1
api-key: $AZURE_OPENAI_API_KEY
```

- `api-version` is **required** on the date path; optional (`v1`/`preview`) on
  the v1 path. Pin a specific GA date in production; re-verify live.
- Entra ID: replace `api-key` with `Authorization: Bearer $AZURE_OPENAI_AUTH_TOKEN`.
- `{deployment}` = the deployment name you created, **not** the underlying model
  id. On the v1 path, `model` in the body carries the deployment/model name.

## Request contract

```json
{
  "model": "my-gpt5-deployment",
  "messages": [
    {"role": "system", "content": "instructions"},
    {"role": "user", "content": "..."}
  ],
  "tools": [],
  "tool_choice": "auto",
  "response_format": {"type": "json_schema", "json_schema": {"name": "out", "strict": true, "schema": {}}},
  "max_completion_tokens": 4096,
  "temperature": 1,
  "stream": false,
  "stream_options": {"include_usage": true}
}
```

- On the **deployment path**, `model` is accepted but the deployment in the URL
  decides the served model; keep them consistent or omit `model`.
- Roles: `system`/`developer`, `user`, `assistant`, `tool`. Reasoning models
  (gpt-5 family, o-series) require `developer` over `system` and reject
  `temperature`/`presence_penalty`/`frequency_penalty`.
- Use `max_completion_tokens` for reasoning models; legacy `max_tokens` is
  rejected by the gpt-5 family (LiteLLM `requires_max_completion_tokens`).
- `response_format` support is **api-version gated**: LiteLLM tracks
  `API_VERSION_MONTH_SUPPORTED_RESPONSE_FORMAT` (>= month 8 of the version year).
  Older `api-version` values 400 on Structured Outputs - pin a recent GA date.

## Response contract

```json
{
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "...", "tool_calls": [], "refusal": null},
    "finish_reason": "stop",
    "content_filter_results": {}
  }],
  "prompt_filter_results": [{"prompt_index": 0, "content_filter_results": {}}],
  "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
}
```

- `finish_reason`: `stop` | `length` | `tool_calls` | `content_filter`.
- Azure adds `prompt_filter_results` (per prompt) and per-choice
  `content_filter_results` annotations on **200** responses - absent on public
  OpenAI. See `errors.md` for the 400 `content_filter` shape.
- `content` is **null** when only tool calls are emitted - check before reading.

## Workflow

1. Confirm resource name, deployment name, `api-version`, and auth method.
2. curl-proof a minimal non-streaming request (no tools) before adding tools/streaming.
3. Add `response_format` only if your pinned `api-version` supports it.
4. For streaming, follow `stream.md` (SSE parity with public OpenAI + `[DONE]`).
5. Track returned `usage` when available; Azure bills per deployed
   model/token.

## Edge cases

- **Deployment != model**: one deployment binds to one model version; renaming
  a deployment or swapping its model changes behavior without code changes.
  Pin deployment -> model mapping in config. See `deployments-and-models.md`.
- `content_filter` as `finish_reason` on a **200**: the completion was cut off
  mid-generation by the output filter - text is partial, not a hard error.
- Reasoning tokens (gpt-5/o-series) are billed but invisible in `content`; check
  `usage.completion_tokens_details.reasoning_tokens` when present.
- `n > 1` multiplies cost and is rarely needed; some deployments cap `n`.
- `api-version` drift: preview features (tool search, computer use) may require
  `preview`; GA features prefer a dated GA version. Mixing is per-request.

## Error handling

Same retry policy as `errors.md`. Azure-specific: 400 `content_filter` (never
retry unchanged - modify prompt or adjust filter config), 401/403 (key/Entra
RBAC), 429 with `Retry-After` (PTU or standard quota). See `errors.md` for the
full envelope including `innererror`.
