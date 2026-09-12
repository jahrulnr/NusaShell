# Bedrock — Converse API

Primary text/agent surface. The normalized Converse API works across all
Bedrock models that support messages (verified 2026-09-11).

- Endpoint: `POST https://bedrock-runtime.{region}.amazonaws.com/model/{modelId}/converse`
- Stream: `POST …/model/{modelId}/converse-stream` -> [stream.md](stream.md)
- Auth: SigV4 (service `bedrock`) **or** `Authorization: Bearer $AWS_BEARER_TOKEN_BEDROCK`
- Docs: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html

`modelId` is URL-encoded; slashes in inference profile ids (`us.anthropic...`)
are encoded as `%3A`-safe or passed raw depending on SDK. With raw HTTP, URL-
encode colons in `:0` suffixes.

## Positive case (curl, SigV4)

```bash
curl -X POST \
  "https://bedrock-runtime.us-east-1.amazonaws.com/model/us.anthropic.claude-3-5-haiku-20241022-v1:0/converse" \
  -H "Content-Type: application/json" \
  --aws-sigv4 "aws:amz:us-east-1:bedrock" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -d '{
    "messages": [{"role": "user", "content": [{"text": "Say hello in one short sentence."}]}],
    "inferenceConfig": {"maxTokens": 256}
  }'
```

With a bearer token instead of SigV4, replace the `--aws-sigv4`/`--user` flags
with `-H "Authorization: Bearer $AWS_BEARER_TOKEN_BEDROCK"`.

## Request contract

```json
{
  "messages": [
    {"role": "user", "content": [{"text": "..."}]}
  ],
  "system": [{"text": "system / developer instructions"}],
  "inferenceConfig": {
    "maxTokens": 1024,
    "stopSequences": [],
    "temperature": 0.7,
    "topP": 0.9
  },
  "additionalModelRequestFields": {"top_k": 200},
  "toolConfig": {"tools": [{"toolSpec": {"name": "...", "inputSchema": {"json": {}}}}]},
  "guardrailConfig": {"guardrailIdentifier": "...", "guardrailVersion": "1"},
  "additionalModelResponseFieldPaths": ["/stop_sequence"],
  "requestMetadata": {"team": "platform"},
  "promptVariables": {"genre": {"text": "pop"}},
  "serviceTier": {"type": "default"},
  "performanceConfig": {"latency": "optimized"}
}
```

| Field | Notes |
|---|---|
| `messages[].role` | `user` or `assistant` only (not `system` / `tool`) |
| `messages[].content` | Array of `ContentBlock`: `text`, `image`, `document`, `toolUse`, `toolResult`, `reasoningContent`, `guardContent`, `video`, `audio` |
| `system` | Top-level array of `SystemContentBlock` (`text`, `cachePoint`); not a message role |
| `inferenceConfig.maxTokens` | **Set explicitly.** Omitting it can yield a low model default -> early `max_tokens` truncation (agent loops break). |
| `inferenceConfig.temperature` | Some models reject it (Claude Opus 5/4.8/4.7 with adaptive thinking) -> omit when unsupported. |
| `additionalModelRequestFields` | Model-specific params not in the base set (e.g. Anthropic `top_k`). |
| `toolConfig` | See [tools.md](tools.md). |
| `serviceTier` | `default` / `flex` / `priority` / `reserved`; not all models support all tiers. |
| `requestMetadata` | Key-value tags recorded in invocation logs (also settable via `X-Amzn-Bedrock-Request-Metadata` header). |

## Response contract

```json
{
  "output": {
    "message": {
      "role": "assistant",
      "content": [
        {"text": "visible answer"},
        {"toolUse": {"toolUseId": "...", "name": "...", "input": {}}},
        {"reasoningContent": {"reasoningText": {"text": "...", "signature": "..."}}}
      ]
    }
  },
  "stopReason": "end_turn",
  "usage": {
    "inputTokens": 125,
    "outputTokens": 60,
    "totalTokens": 185,
    "cacheReadInputTokens": 0,
    "cacheWriteInputTokens": 0
  },
  "metrics": {"latencyMs": 1175},
  "additionalModelResponseFields": {},
  "serviceTier": {"type": "default"}
}
```

`stopReason` (common): `end_turn` | `tool_use` | `max_tokens` |
`stop_sequence` | `content_filter` | `refusal` | `guardrail_stop`.
Map for OpenAI-shaped loops: end_turn->stop, tool_use->tool_calls,
max_tokens->length, content_filter/refusal->content_filter.

`usage` lives at top level (not nested in `output`). Always track it.

## Workflow

1. Prove with curl using SigV4 (`--aws-sigv4`) or a bearer token.
2. Pin a model id / inference profile from [model-list.md](model-list.md);
   re-check `ListFoundationModels` / `ListInferenceProfiles`.
3. Always set `inferenceConfig.maxTokens` for agent/tool turns.
4. If tools: send `toolConfig`, parse `toolUse` blocks, run tool, append a
   `user` message with `toolResult` blocks, re-call ([tools.md](tools.md)).
5. If reasoning: capture `reasoningContent` + `signature`; replay the full
   assistant message (including reasoning blocks) unmodified in later turns.
6. Track `usage` (include `cacheReadInputTokens`/`cacheWriteInputTokens`).

## Edge cases

- **Converse vs InvokeModel**: Converse normalizes across models but drops
  most model-native response fields. Use `additionalModelResponseFieldPaths`
  to surface specific native fields, or fall back to InvokeModel
  (`/model/{id}/invoke`) with the model's raw native body when you need full
  provider-specific control.
- **Anthropic Messages passthrough**: on `bedrock-runtime`, the `/anthropic`
  route accepts Anthropic-native Messages bodies
  (`anthropic_version: "bedrock-2023-05-31"`, `max_tokens`, `messages`) with
  SigV4 or bearer token. Use this for Anthropic-native tool types
  (`computer_*`, `bash_*`, `text_editor_*`, `memory_*`) that Converse does not
  expose. The `bedrock-mantle` endpoint also serves `/anthropic/v1/messages`.
- **Mistral / Meta models**: Converse embeds your input in a model-specific
  prompt template automatically — do not add your own chat template.
- **Prompt management prompts**: when `modelId` is a prompt ARN, you cannot
  include `additionalModelRequestFields`, `inferenceConfig`, `system`, or
  `toolConfig`; use `promptVariables` instead.
- **Role alternation**: messages must alternate user/assistant. Merge adjacent
  same-role messages (required when appending multiple `toolResult` blocks).
- **`toolResult` goes in a `user` message**, not a `tool` role (no `tool` role
  exists in Converse).
- **Service tier errors are misleading**: an unsupported `serviceTier` can
  return "The provided model identifier is invalid" rather than naming the
  tier. Check tier support before assuming the model id is wrong.
- **Claude Opus 5/4.8/4.7 + temperature**: Bedrock rejects `temperature` for
  these models with adaptive thinking active. Omit it.

## Error handling

See [errors.md](errors.md). Never blind-retry `ValidationException` (400) or
`AccessDeniedException` (403). Retry only `ThrottlingException` (429),
`ServiceUnavailableException` (503), `InternalServerException` (500),
`ModelNotReadyException`, `ModelTimeoutException` (408).
