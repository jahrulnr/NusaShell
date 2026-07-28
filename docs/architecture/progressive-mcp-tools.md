# Progressive MCP tools

The agent must not receive every MCP schema at the start of a turn. One server
may expose hundreds of tools while providers commonly impose function-count
and prompt-size limits.

## Initial tool set

Every turn starts with ten shell-owned meta-tools:

- `mcp_list` — list installed MCP plugins with runtime state and autostart.
- `mcp_enable` — start one plugin MCP through `PluginRuntimeManager`.
- `mcp_disable` — stop one running plugin MCP through
  `PluginRuntimeManager`.
- `tool_search` — search a running plugin's MCP tool names/descriptions with a
  bounded result set.
- `tool_list` — list all tool names and descriptions from a running MCP plugin
  without a search query.
- `tool_schema` — return one tool schema and grant that concrete tool to the
  following round.
- `mcp_context` — access non-tool MCP context through an explicit action:
  `list_prompts`, `get_prompt`, `search_resources`,
  `list_resource_templates`, `complete`, or `read_resource`.
- `docs_search` — search the internal Markdown documentation corpus for
  keyword-scored chunks.
- `docs_list` — list all documents in the internal documentation corpus.
- `docs_read` — read a full document, or a single chunk by `chunk_id`, with
  optional `max_chars`/`offset` pagination.

`tool_schema` is the bridge to execution: after discovery, the actual MCP tool
is added to the next provider request and called through the normal typed
broker path. Its grant is scoped by turn trace ID and is removed in a `finally`
cleanup, so concurrent conversations cannot share tool permissions and the
catalog cannot grow across turns. There is no untyped catch-all `tool_call`
function.

`mcp_context` keeps prompts and resources out of the launcher UI and out of the
initial prompt payload. Prompt arguments remain strings and required arguments
are validated. Prompt/resource search is capped at 20 results; text resource
reads are capped at 50 KB. Binary resource content is not injected into the
provider.

The Agent UI intentionally has no MCP catalog, scope selector, prompt browser,
or resource attachment panel. The model can discover, start, stop, and inspect
MCPs when needed; NusaShell remains the only broker.

## Autostart

Autostart is a user preference per installed plugin, persisted separately from
live runtime state. On backend startup, `PluginRuntimeManager` starts opted-in
plugins independently. One failure is logged and never blocks shell startup.
