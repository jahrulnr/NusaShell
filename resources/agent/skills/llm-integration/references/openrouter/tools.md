# OpenRouter — Tools (function calling)

OpenAI-compatible `tools` / `tool_choice` on chat completions. Confirm
`supported_parameters` includes `tools` (and `tool_choice` / structured outputs
as needed) via `model-list.md` before enabling.

## Positive case

```bash
curl https://openrouter.ai/api/v1/chat/completions \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic/claude-sonnet-4",
    "messages": [{"role":"user","content":"What is the weather in Paris?"}],
    "tools": [{
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get current weather for a city",
        "parameters": {
          "type": "object",
          "properties": { "city": { "type": "string" } },
          "required": ["city"]
        }
      }
    }],
    "tool_choice": "auto"
  }'
```

Assistant may return `tool_calls[]`; send `role: "tool"` results back in the
next turn (same OpenAI loop). Cap steps/tool calls and spend (cross-provider
skill rule).

## Provider routing with tools

When using `provider` / `models[]` fallbacks:

- Prefer providers that advertise tool support on the endpoints API.
- A fallback provider that strips tools → surprising plain-text answers —
  restrict `provider.only` or filter endpoints with `tools` in
  `supported_parameters`.

## Edge cases

- Hallucinated tool names/args — validate before executing.
- Parallel tool calls — handle array order; don't assume single call.
- Strict JSON schema support varies by upstream provider — invalid schema may
  400 on some routes and be softened on others.
- Destructive tools — human confirmation required unless explicit policy
  authorization already covers the action (cross-provider rule).

## Related

Agent loop patterns → `../references/_shared/usecase-patterns.md`.
Errors → `errors.md`.
