# Progressive MCP tools

The agent must not receive every MCP tool schema at the start of a turn. A
single MCP server may expose hundreds of tools while some model providers cap
the number of functions in one request.

## Initial tool set

Every turn starts with only these shell-owned tools:

- `mcp_list` — lists installed MCP plugins with runtime state and autostart.
- `mcp_enable` — starts one enabled plugin MCP through `PluginRuntimeManager`.
- `mcp_disable` — stops one running plugin MCP through `PluginRuntimeManager`.
- `tool_search` — searches a running plugin's MCP tool names/descriptions with
  a bounded result set.
- `tool_schema` — returns the schema for one search result and grants that
  single tool to the following round.
- `resource_search` — searches the concrete text resources exposed by a running
  MCP plugin.
- `resource_read` — reads a discovered resource through the broker. Text is
  capped at 50 KB per read; binary content and resource templates are not
  passed to a provider automatically.

`tool_schema` is the deliberate bridge to execution: once selected, the actual
MCP tool is added to the next provider request. The agent then calls that typed
tool directly through the normal broker path. This keeps validation at the
schema boundary and avoids an untyped catch-all `tool_call` function.

Prompts deliberately stay outside the agent tool catalog. The Agent UI lists
them for a user-selected, running MCP server and inserts the returned messages
only after the user invokes a prompt. Resources are also available in that UI
as explicit attachments to a conversation, in addition to the bounded agent
resource discovery operations above.

## Autostart

Autostart is a user preference per installed plugin, persisted separately from
runtime state. On backend startup, `PluginRuntimeManager` starts only enabled
plugins whose persisted autostart preference is true. Failure is logged per
plugin and never blocks the shell startup.
