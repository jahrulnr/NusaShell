# OpenAI — Function calling (tools)

## Tool schema (both APIs)

```json
{
  "type": "function",
  "name": "get_order_status",
  "description": "When to call this tool, param semantics, units",
  "parameters": {"type": "object", "properties": {...}, "required": [...],
                  "additionalProperties": false},
  "strict": true
}
```

- `strict: true` forces schema-conformant arguments. Requirements: all
  properties listed in `required`, `additionalProperties: false`, flat
  supported keywords. Invalid strict schemas → 400 at request time.
- `description` is the model's only guide — write when/why/units, not just
  what. Constrain with `enum`, `minimum/maximum` wherever possible.

## Responses API flow

1. Send `tools` in the request.
2. Response `output[]` contains `{type: "function_call", call_id, name,
   arguments}` — `arguments` is a **JSON string**, not an object.
3. Execute locally (validate args first).
4. Next request: append to `input`:
   `{type: "function_call_output", call_id, output: "<string>"}`.
5. Model continues with the results.

## Chat Completions flow

1. Response: `message.tool_calls[]` = `{id, type: "function",
   function: {name, arguments: "<json string>"}}`, `finish_reason: "tool_calls"`.
2. Next request: append the assistant message verbatim, then one
   `{role: "tool", tool_call_id, content: "<string>"}` per call.

## Agentic loop (language-agnostic)

```text
MAX_ITER = 10
messages = [system, user]
loop:
  resp = call_model(messages, tools)
  calls = extract_tool_calls(resp)
  if empty(calls): return final_text(resp)
  if iterations >= MAX: return force_final_or_abort()
  for call in calls:                      # parallel-safe calls may run concurrently
    args = parse_json(call.arguments)  # validate; on failure return error to model
    result = execute_tool(call.name, args)  # with timeout + size cap
    append_tool_output(call.call_id, truncate(result))
  if no progress after N identical calls: abort with error
```

## Edge cases

- `arguments` is a JSON **string** and can be malformed — validate, and on
  failure return a tool output like `{"error": "invalid JSON: ..."}` so the
  model can self-correct. Do not crash.
- **Hallucinated tool name**: return an error output naming the valid tools;
  never execute blindly.
- Parallel tool calls: one assistant turn carries multiple `tool_calls` —
  every `call_id`/`tool_call_id` needs exactly one matching output message.
- Token cap mid-arguments → truncated JSON with `finish_reason: "length"`;
  treat as failed call, not a retryable tool error.
- Tool outputs join the context — cap size (truncate/summarize) before
  appending; a huge tool result can blow the next request's context.
- Fallback/parallel tool calls + low max tokens is the classic broken-agent
  combo: cap steps/tool calls AND tokens, and return an explicit incomplete
  state or abort on breach — never claim that unfinished work completed.
- Keep temperature low (or default) for tool use; sampling creativity corrupts
  argument JSON.

## Error handling

Tool execution failure is **not** an API error: report it back as the tool
output so the model can adapt or abort. Reserve HTTP-layer handling for
`errors.md`.
