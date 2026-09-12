# Anthropic — Tools (function calling)

Claude calls tools you define (**client tools**, executed by your app) or tools
Anthropic runs (**server tools**). The wire difference is who executes the call.

## Tool kinds

| Kind | Examples | Executes |
|---|---|---|
| User-defined client tool | your `get_weather` with `input_schema` | Your app |
| Anthropic-schema client tool | `memory`, `bash`, `text_editor`, `computer`, `browser` | Your app (schema + training provided) |
| Server tool | `web_search`, `web_fetch`, `code_execution`, `tool_search`, `advisor` | Anthropic infrastructure |
| MCP connector | remote MCP servers via the API | Anthropic connects, tools proxied |

Server tool `type` strings are versioned (e.g. `web_search_20260209`,
`code_execution_20250825`/`_20260120`) — copy the current string from the live
Tool reference before shipping.

## Request contract

```json
{
  "tools": [
    {
      "name": "get_weather",
      "description": "Get the current weather for a location.",
      "input_schema": {
        "type": "object",
        "properties": {"location": {"type": "string"}},
        "required": ["location"]
      },
      "strict": true,
      "eager_input_streaming": false
    }
  ],
  "tool_choice": {"type": "auto", "disable_parallel_tool_use": false}
}
```

| Field | Notes |
|---|---|
| `input_schema` | JSON Schema; `strict: true` guarantees calls conform exactly (recommended for code-executing tools) |
| `eager_input_streaming` | Per-tool: stream parameter JSON without server-side buffering (fine-grained streaming) |
| `tool_choice.type` | `auto` (default) · `any` (must call some tool) · `tool` (+`name`, forced) · `none` |
| `disable_parallel_tool_use` | `true` → at most one tool call per turn |

## Round trip (client tool)

1. Send the user message + `tools`. Claude replies with
   `stop_reason: "tool_use"` and one or more `tool_use` blocks:
   `{type, id, name, input}` — `input` is a parsed object.
2. Execute the tool(s) in your app. Cap execution time and output size.
3. Append the assistant content **unchanged** (thinking blocks included) and a
   user turn with one `tool_result` per call:

```json
{
  "role": "user",
  "content": [
    {"type": "tool_result", "tool_use_id": "toolu_…", "content": "15°C, partly cloudy"}
  ]
}
```

4. Call again. Repeat until `stop_reason` is no longer `tool_use`; then read the
   `text` blocks.

`tool_result.content` accepts a string or blocks (text/image/document/search
result). Set `"is_error": true` on the result to signal a failed call — let the
model adapt instead of crashing your loop.

## Response blocks

| Block | Meaning |
|---|---|
| `tool_use` | Client tool call (`id`, `name`, `input`) |
| `server_tool_use` | Server tool call (Anthropic executes) |
| `web_search_tool_result` / `web_fetch_tool_result` / `code_execution_tool_result` / `bash_…` / `text_editor_…` / `tool_search_tool_result` | Server tool results, inline in the same response |

Server tool loops can continue server-side. If Claude pauses a long-running
turn, the response comes back with `stop_reason: "pause_turn"` — send the
response back **as-is** to let the model continue.

## Workflow

1. Curl one tool call with a single minimal tool; confirm the `tool_use` block.
2. Build the loop: extract `tool_use` → validate `input` → execute → append
   `tool_result` → repeat, with a max-iteration cap and per-tool timeout.
3. Add `strict: true` to tools that mutate anything (files, infra, money).
4. Keep thinking blocks on the assistant turn when thinking is on — see
   [thinking.md](thinking.md) (this is mandatory, not optional).
5. Log a redacted audit event: `id`, `name`, bounded outcome metadata, and
   returned `usage`; do not log raw `input` or result by default.

## Edge cases

- **Thinking + tools = pass blocks back.** Every `thinking` / `redacted_thinking`
  block from the assistant turn must be returned unchanged (including empty
  `thinking` fields). Filtering blocks by `type == "thinking"` and dropping
  `redacted_thinking` breaks the loop with a 400.
- **Forced tool use vs thinking:** `any` / `tool` are incompatible with manual
  `thinking.type: "enabled"`; adaptive thinking supports them — except on
  Claude Fable 5.1 / Mythos 5.1, which reject forced tool use entirely (use
  `auto` + `strict`).
- **Parallel calls:** a response can carry several `tool_use` blocks; answer
  all of them in one user turn. A run of consecutive `tool_use`/`tool_result`
  blocks counts as one block for cache lookback (see
  [prompt-caching.md](prompt-caching.md)).
- **Missing required params:** Opus asks; Sonnet may hallucinate a plausible
  value (e.g. a default location). Validate inputs server-side regardless.
- **Tool definitions cost tokens.** Tool-use adds a hidden system prompt
  (roughly 300–800 tokens on current models depending on `tool_choice`) plus
  the schema itself — cache tool definitions (they're cacheable).
- **Server tool charges:** tokens plus usage-based fees (web search per
  search, etc.) — surfaced in `usage.server_tool_use`.

## Pricing & errors

Client tools cost only tokens. Server tools add usage-based charges — check the
per-tool pages. Errors: tool-input validation failures surface as 400s naming
the tool; transient failures follow [errors.md](errors.md).
