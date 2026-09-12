# Azure OpenAI - Function calling (tools)

Tool calling on Azure mirrors public OpenAI semantics with two Azure-specific
wire differences on the Responses API. Read this before writing any tool code.

Verified 2026-09-11 (LiteLLM `llms/azure/chat/gpt_transformation.py`,
`llms/azure/responses/transformation.py`; Azure OpenAI reference).

## Endpoint + auth

Same as `chat-completions.md` (tools ride the chat body) or `responses.md`
(tools ride the Responses body). Auth unchanged.

## Request contract

Chat Completions tool schema (same as public OpenAI):

```json
{
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_order_status",
      "description": "When/why/units",
      "parameters": {"type": "object", "properties": {...}, "required": [...], "additionalProperties": false},
      "strict": true
    }
  }],
  "tool_choice": "auto"
}
```

Responses API tool schema - **Azure requires flattened tools**:

```json
{
  "tools": [{
    "type": "function",
    "name": "get_order_status",
    "description": "When/why/units",
    "parameters": {"type": "object", "properties": {...}, "required": [...], "additionalProperties": false},
    "strict": true
  }]
}
```

- On Azure Responses, params live at the **top level**, not nested under
  `function`. LiteLLM pops `function` and merges its fields onto the tool object
  (`transform_responses_api_request`). Sending the nested public-OpenAI shape
  can 400. Chat Completions keeps the nested `function` shape.
- `strict: true` forces schema-conformant arguments (all props in `required`,
  `additionalProperties: false`, supported keywords only). Invalid strict
  schemas 400 at request time.
- `description` is the model's only guide - write when/why/units, constrain with
  `enum`/`minimum`/`maximum`.

## Response contract

Chat Completions: `message.tool_calls[]` =
`{id, type: "function", function: {name, arguments: "<json string>"}}`,
`finish_reason: "tool_calls"`.

Responses: `output[]` contains
`{type: "function_call", call_id, name, arguments: "<json string>"}`.

- `arguments` is a JSON **string** in both APIs - validate before executing.

## Workflow

Chat Completions loop:
1. Response carries `tool_calls[]` + `finish_reason: "tool_calls"`.
2. Execute each call locally (validate args; timeout + size cap).
3. Next request: append the assistant message verbatim, then one
   `{role: "tool", tool_call_id, content: "<string>"}` per call.

Responses loop:
1. Response `output[]` carries `function_call` items.
2. Execute locally.
3. Next request: append to `input`
   `{type: "function_call_output", call_id, output: "<string>"}`.
4. **Strip `status`** from any replayed message/reasoning items (Azure rejects it).

Agentic loop (language-agnostic): same skeleton as `../openai/tools.md` - cap
iterations (MAX_ITER ~10), parse + validate args, return error outputs to the
model on failure, force a final answer on breach.

## Edge cases

- **Flattened vs nested tools**: if you port code from public OpenAI Responses
  to Azure Responses, flatten the tool schema or expect 400s. Chat Completions
  tool schemas are unchanged.
- **Hallucinated tool name**: return an error output naming valid tools; never
  execute blindly.
- Parallel tool calls: one turn carries multiple calls - every `call_id`/
  `tool_call_id` needs exactly one matching output message.
- Token cap mid-arguments -> truncated JSON with `finish_reason: "length"` -
  treat as failed call, not a retryable tool error.
- Tool outputs join context - cap size (truncate/summarize) before appending.
- `tool_search` (Responses, preview): load tool definitions on demand instead
  of sending all tools upfront - reduces token spend for large tool catalogs.
- Keep temperature low/default for tool use; sampling creativity corrupts JSON.

## Error handling

Tool execution failure is **not** an API error: report it back as the tool
output (`{"error": "invalid JSON: ..."}`) so the model can self-correct.
Reserve HTTP-layer handling (400 bad tool schema, 400 `content_filter`, 429)
for `errors.md`.
