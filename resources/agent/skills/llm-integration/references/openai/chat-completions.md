# OpenAI — Chat Completions (legacy)

Use only when maintaining existing integrations or when a feature is
Chat-Completions-only. New code → Responses API.

- Endpoint: `POST https://api.openai.com/v1/chat/completions`
- Auth: `Authorization: Bearer $OPENAI_API_KEY`

## Request contract

```json
{
  "model": "gpt-5.4",
  "messages": [
    {"role": "developer", "content": "system instructions"},
    {"role": "user", "content": "..."}
  ],
  "tools": [],
  "tool_choice": "auto",
  "response_format": {"type": "text"},
  "max_completion_tokens": 4096,
  "temperature": 1,
  "stream": false,
  "stream_options": {"include_usage": true}
}
```

- Roles: `developer` (system-level; replaces `system` for reasoning models),
  `user`, `assistant`, `tool`.
- `content` may be a string or parts array (`text`, `image_url`, `audio`).
- `response_format`: `{"type": "json_object"}` (loose JSON mode) or
  `{"type": "json_schema", "json_schema": {name, strict: true, schema}}`
  (Structured Outputs).
- Use `max_completion_tokens`, not `max_tokens` (deprecated; rejected by
  reasoning models).

## Response contract

```json
{
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "...", "tool_calls": [], "refusal": null},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
}
```

`finish_reason`: `stop` (natural end) | `length` (token cap hit) |
`tool_calls` (model wants tools) | `content_filter`.

## Edge cases

- `content` is **null** when the model emits only tool calls — never assume
  non-null before reading.
- `n > 1` returns multiple choices and multiplies cost — rarely needed.
- JSON mode (`json_object`) silently degrades or errors if the prompt never
  mentions JSON; prefer `json_schema` with `strict: true`.
- Strict schema rules: every property in `required`, `additionalProperties:
  false`, no unsupported keywords (`$ref` to external docs, etc.). Invalid
  strict schemas return 400.
- `finish_reason: "length"` means truncated output — for JSON this is a
  parse failure; raise `max_completion_tokens` or shorten input.
- Reasoning models ignore/`400` on `temperature`, `presence_penalty`,
  `frequency_penalty`; reasoning tokens are billed but invisible in `content`.

## Error handling

Same error envelope and retry policy as `errors.md`.
