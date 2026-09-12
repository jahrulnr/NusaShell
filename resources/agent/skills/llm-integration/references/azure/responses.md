# Azure OpenAI - Responses API

The Responses API on Azure unifies chat + tool use in one stateful surface,
mirroring public OpenAI `/v1/responses`. Prefer this for new builds on Azure.

Verified 2026-09-11 (Azure OpenAI Responses API how-to, updated 2026-08-18;
LiteLLM `llms/azure/responses/transformation.py`).

## Endpoint + auth

```bash
# v1 stable path (recommended) - model in body, api-version optional (defaults v1)
POST https://{resource}.openai.azure.com/openai/v1/responses?api-version=v1
api-key: $AZURE_OPENAI_API_KEY

# Date-versioned path (LiteLLM default route)
POST https://{resource}.openai.azure.com/openai/responses?api-version=preview
api-key: $AZURE_OPENAI_API_KEY
```

- Entra ID: `Authorization: Bearer $AZURE_OPENAI_AUTH_TOKEN` (scope
  `https://ai.azure.com/.default` or `https://cognitiveservices.azure.com/.default`).
- WebSocket (realtime Responses): `wss://{resource}.openai.azure.com/openai/v1/responses`
  with **no** `api-version` query param; auth via `Authorization` header; model
  sent in the `response.create` body, not the URL (LiteLLM `get_websocket_url`).
- Supported regions are a subset of all Azure OpenAI regions - verify your
  resource region supports Responses before deploying (see how-to region list).

## Request contract

```json
{
  "model": "my-gpt5-deployment",
  "input": [
    {"role": "developer", "content": "instructions"},
    {"role": "user", "content": "..."}
  ],
  "tools": [],
  "tool_choice": "auto",
  "reasoning": {"effort": "medium"},
  "max_output_tokens": 4096,
  "stream": false
}
```

- `model` = deployment name (v1 path) or model id. `input` is a string or an
  array of input items (messages, function_call, function_call_output, reasoning).
- **Azure-specific tool flattening**: tools must be **flattened** - params at the
  top level, not nested under `function`. LiteLLM pops `function` and merges its
  fields onto the tool object before sending. (Public OpenAI accepts the nested
  `{type:"function", function:{...}}` shape; Azure Responses does not.)
- **No `context_management`**: Azure Responses API does **not** support
  compaction / `context_management` (LiteLLM `AZURE_UNSUPPORTED_PARAMS`). Do not
  send it; use the Codex `/responses/compact` surface separately if needed.
- **No `status` field on input items**: Azure rejects `status` on `message` and
  `reasoning` input items (LiteLLM filters it). Strip `status` before replay.
- Reasoning items: when replaying encrypted reasoning, Azure validates against
  the original item id - keep item ids stable across replay (OpenClaw Foundry
  sets `replayResponsesItemIds: true`).

## Response contract

```json
{
  "id": "resp_...",
  "object": "response",
  "model": "my-gpt5-deployment",
  "output": [
    {"type": "reasoning", "id": "rs_...", "summary": [...]},
    {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "..."}]},
    {"type": "function_call", "id": "fc_...", "name": "get_x", "arguments": "{\"q\":\"...\"}"}
  ],
  "usage": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "input_tokens_details": {"cached_tokens": 0}},
  "status": "completed"
}
```

- `output[]` is ordered: reasoning (if any) -> message and/or function_call items.
- `arguments` on `function_call` is a JSON **string** - validate before executing.
- `status`: `completed` | `in_progress` | `failed` | `incomplete` | `cancelled`.
- Failure paths: `status: "failed"` with `error` populated; streams can end
  without `response.completed` (see `stream.md`).

## Workflow

1. Confirm resource + deployment + region supports Responses.
2. curl-proof a minimal `input` string request (no tools, no reasoning).
3. Add tools with the **flattened** schema; strip `status` from replayed items.
4. For multi-turn, append `function_call_output` items with matching `call_id`.
5. Stream via `stream.md` (typed events, same sequence as public OpenAI).

## Edge cases

- **`store` / persisted responses**: Azure supports retrieving/deleting/canceling
  responses by id (`GET/DELETE /openai/responses/{id}?api-version=...`), but
  Foundry's persisted-store semantics differ from public OpenAI - verify
  `store` behavior for your region/model before relying on `previous_response_id`.
- **Not supported today**: image generation via multi-turn editing + streaming;
  file upload purpose `user_data` for PDFs (known issue per how-to).
- **Tool search** and **multi-agent orchestration** are Responses-only features
  on Azure (preview) - load tool definitions on demand; delegate to subagents.
- `computer-use-preview` model is reached via Responses, not Chat Completions.
- Background mode + streaming has known performance issues - avoid combining.

## Error handling

Same envelope + retry policy as `errors.md`. Azure-specific: 400 on
unflattened tools or `status`-bearing items (fix request, never retry); 400
`content_filter` with `innererror`; 429 with `Retry-After`. See `errors.md`.
