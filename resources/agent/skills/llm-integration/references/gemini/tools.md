# Gemini — Function calling (tools)

Native tools use `functionDeclarations` + `functionCall` / `functionResponse`
parts — not OpenAI `tools[].type=function` / `tool_calls[]` wire shape
(though agent runtimes often translate).

## Declaration schema

```json
{
  "tools": [{
    "functionDeclarations": [{
      "name": "get_order_status",
      "description": "When to call; param units",
      "parameters": {
        "type": "object",
        "properties": {
          "order_id": {"type": "string", "description": "…"}
        },
        "required": ["order_id"]
      }
    }]
  }],
  "toolConfig": {
    "functionCallingConfig": {
      "mode": "AUTO"
    }
  }
}
```

| `functionCallingConfig.mode` | Meaning |
|---|---|
| `AUTO` | Model chooses (≈ OpenAI `tool_choice: auto`) |
| `ANY` | Must call a function (≈ `required`); optional `allowedFunctionNames` |
| `NONE` | Disable tools for this turn |

## Schema subset (critical)

Gemini `Schema` is a **subset** of OpenAPI / JSON Schema. Strip or rewrite
before send (Hermes `sanitize_gemini_tool_parameters` / OpenClaw
`clean-for-gemini`):

- Drop: `$schema`, `$ref`, `additionalProperties`, `patternProperties`,
  unsupported constraint keywords that the API rejects.
- Keep commonly: `type`, `description`, `properties`, `required`, `items`,
  `enum`, `nullable`, min/max length/items/properties, `anyOf`, …
- Non-string `enum` values → stringify.
- `required` entries that are not in `properties` → prune.

Invalid declarations → **400**. Fix the schema; never retry unchanged.

## Agentic loop

```text
MAX_ITER = 10
history = [user turn…]
loop:
  resp = generateContent(contents=history, tools=…, thinkingConfig=…)
  calls = extract functionCall parts from candidates[0].content.parts
  if empty(calls): return visible text parts
  if iterations >= MAX: abort
  # Append the model content verbatim (including thoughtSignature fields)
  history.append(model_content)
  # One user content with one functionResponse part per call
  # (merge parallel responses into a single user turn — same-role merge)
  history.append({role:user, parts:[functionResponse…]})
```

### Thought signatures on tool turns

Gemini 3 **requires** replaying `thoughtSignature` from `functionCall`
parts on the next turn. Omitting them → 400 or degraded tool follow-up.
See [thinking.md](thinking.md). Official SDKs manage this if you pass
history unmodified; raw REST clients must copy signatures byte-for-byte.

## Edge cases

- Parallel tool calls → multiple `functionCall` parts in one model content;
  respond with matching `functionResponse` parts in **one** user content
  (adjacent same-role merge).
- Hallucinated name → return `functionResponse` with an error payload naming
  valid tools; do not execute.
- Truncated args (`MAX_TOKENS`) → do not parse/execute; raise budget.
- Live / realtime tool names have extra constraints (leading letter, max
  length) — [realtime.md](realtime.md).
- Google Search grounding is a **built-in tool**, not a custom declaration —
  OpenClaw exposes it as the `gemini` web-search provider.

## Error handling

Tool execution failure ≠ HTTP error: send it back as `functionResponse`.
HTTP/schema issues → [errors.md](errors.md).
