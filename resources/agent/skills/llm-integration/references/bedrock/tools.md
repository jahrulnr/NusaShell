# Bedrock — Tool use (toolConfig / toolUse / toolResult)

Client-side tool calling via the Converse API. The model returns a `toolUse`
request; your code runs the tool and returns a `toolResult` in a follow-up
`user` message. Verified 2026-09-11.

- Endpoint: same as [converse.md](converse.md) (`/converse` or `/converse-stream`)
- Auth: SigV4 or bearer token (see [README.md](README.md))
- Docs: https://docs.aws.amazon.com/bedrock/latest/userguide/tool-use.html ,
  https://docs.aws.amazon.com/bedrock/latest/userguide/tool-use-client-side.html

## Endpoint + auth

Tool use rides the Converse request body (`toolConfig` field). No separate
endpoint. Streaming tool use uses ConverseStream — tool deltas arrive as
`contentBlockDelta` with `toolUse.input` partial JSON (see [stream.md](stream.md)).

## Request contract — toolConfig

```json
{
  "messages": [{"role": "user", "content": [{"text": "What is the top song on WZPZ?"}]}],
  "toolConfig": {
    "tools": [
      {
        "toolSpec": {
          "name": "top_song",
          "description": "Get the most popular song on a radio station.",
          "inputSchema": {
            "json": {
              "type": "object",
              "properties": {
                "sign": {"type": "string", "description": "Call sign, e.g. WZPZ"}
              },
              "required": ["sign"]
            }
          }
        }
      }
    ],
    "toolChoice": {"tool": {"name": "top_song"}},
    "disableParallelToolUse": true
  }
}
```

| Field | Notes |
|---|---|
| `tools[].toolSpec.name` | Must match `^[a-zA-Z][a-zA-Z0-9_]*$` and be unique. Bedrock sanitizes invalid names (LiteLLM: `make_valid_bedrock_tool_name`). |
| `tools[].toolSpec.inputSchema.json` | A JSON Schema object. Bedrock supports a **subset** of JSON Schema (no `$ref`, limited formats). Keep schemas simple. |
| `toolChoice` | Omit for auto. `{"auto": {}}` = model decides; `{"any": {}}` = force a tool call; `{"tool": {"name": "x"}}` = force specific tool. |
| `toolChoice.auto` | Optional sub-field `disableParallelToolUse: true` to force single tool call. |
| `disableParallelToolUse` | Top-level convenience; some models also accept it via `additionalModelRequestFields`. Do not send both `toolConfig.toolChoice` and a conflicting `additionalModelRequestFields.tool_choice` (Converse rejects the combination). |

## Response contract — toolUse block

When the model wants a tool, `stopReason` is `tool_use` and the assistant
message contains a `toolUse` content block:

```json
{
  "output": {
    "message": {
      "role": "assistant",
      "content": [
        {"text": "Let me check that for you."},
        {"toolUse": {"toolUseId": "tool_use_abc123", "name": "top_song", "input": {"sign": "WZPZ"}}}
      ]
    }
  },
  "stopReason": "tool_use",
  "usage": {"inputTokens": 50, "outputTokens": 30, "totalTokens": 80}
}
```

| Field | Notes |
|---|---|
| `toolUse.toolUseId` | Stable id for this call. Echo it back in `toolResult.toolUseId`. |
| `toolUse.name` | The tool spec name (may be sanitized if your original was invalid). |
| `toolUse.input` | A **parsed JSON object** in non-streaming Converse. In ConverseStream it arrives as **partial JSON strings** across `contentBlockDelta` events. |
| Multiple `toolUse` blocks | Possible when parallel tool use is allowed — one block per call, distinct `toolUseId`s. |

## Workflow (tool loop)

1. Send the user message + `toolConfig`.
2. Receive response. If `stopReason == "tool_use"`:
   a. Append the full assistant `output.message` to `messages` (unmodified,
      including any `reasoningContent` blocks + signatures).
   b. For each `toolUse` block, run the tool locally.
   c. Build a **single `user` message** whose `content` is an array of
      `toolResult` blocks (one per tool call):
      ```json
      {"role": "user", "content": [
        {"toolResult": {
          "toolUseId": "tool_use_abc123",
          "content": [{"json": {"song": "Elemental Hotel", "artist": "8 Storey Hike"}}],
          "status": "success"
        }}
      ]}
      ```
   d. Append that user message to `messages` and re-call Converse with the
      same `toolConfig`.
3. Repeat until `stopReason` is `end_turn` / `max_tokens` / etc.
4. Cap steps/tool calls and spend (cross-provider rule) — unbounded tool loops
   burn cost.

`toolResult.content` block types: `json` (structured), `text` (plain),
`image`, `document`. `status` is `success` (default) or `error`.

## Edge cases

- **No `tool` role.** Tool results go in a `user` message with `toolResult`
  blocks, unlike OpenAI's `role: "tool"`. This is the #1 porting mistake.
- **Multiple tool results in one user message.** When the model made parallel
  calls, put all `toolResult` blocks in a single `user` message (role
  alternation requires it — you cannot send two consecutive `user` messages).
- **Replay reasoning blocks.** If the assistant message that requested the
  tool contained `reasoningContent` blocks with a `signature`, you **must**
  include them unmodified in the replayed assistant message. Dropping or
  altering them throws a validation error on the next turn.
- **Empty tool input.** Some models emit `toolUse.input` as `{}` or an empty
  string. Handle both: treat empty string as `{}`.
- **Tool name sanitization.** Bedrock rejects names with hyphens, spaces, or
  leading digits. Sanitize before sending; map the sanitized name back when
  dispatching the result.
- **Structured outputs.** Bedrock supports validated JSON via tool use
  (`structured output` mode) — see AWS "Get validated JSON results from
  models". Use `toolChoice.tool` + a single tool whose schema is your output
  shape.
- **Anthropic-native tool types** (`computer_use_preview`, `bash_*`,
  `text_editor_*`, `memory_*`) are **not** available via Converse `toolSpec`.
  Use the Anthropic Messages passthrough (`/anthropic` route) for those.
- **Server-side tool use** (Lambda / AgentCore Gateway invoked by Bedrock
  itself) is currently only on the Responses API, not Converse.

## Error handling

See [errors.md](errors.md). A `ValidationException` on a tool request usually
means: invalid tool name, unsupported JSON Schema feature, conflicting
`toolChoice` + `additionalModelRequestFields.tool_choice`, or a
tampered/missing `reasoningContent.signature` on replay. Fix the request; do
not retry as-is.
