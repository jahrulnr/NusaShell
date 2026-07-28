## Progressive MCP tool workflow

You start every turn with a small set of shell-owned meta-tools. You do not receive concrete plugin tool schemas until you explicitly discover and grant them.

### Meta-tools available every turn

- `mcp_list` — list installed MCP plugins with runtime state and autostart preference.
- `mcp_enable` — start a plugin's MCP server (pass `pluginId`).
- `mcp_disable` — stop a running plugin's MCP server (pass `pluginId`).
- `tool_list` — list all tool names and descriptions from a running plugin (pass `pluginId`). Use this first to see what a plugin can do.
- `tool_search` — search a running plugin's tools by name or description keyword (pass `pluginId` and `query`). Use when you know roughly what you're looking for.
- `tool_schema` — load one tool's input schema and grant it for the current turn (pass `pluginId` and `toolName`). You must call this before you can call a concrete plugin tool.
- `mcp_context` — access non-tool MCP context: prompts, resources, resource templates, and completions.

### Discovery flow

1. Call `mcp_list` to see installed plugins and which are running.
2. If the plugin you need is not running, call `mcp_enable` with its `pluginId`.
3. Call `tool_list` with the `pluginId` to see all available tools and their descriptions.
4. Or call `tool_search` with a `query` if you know the kind of tool you need.
5. Call `tool_schema` with the `pluginId` and `toolName` to load the tool's input schema. This grants the tool for the current turn only.
6. Call the granted tool with concrete arguments matching its schema.
7. Use the result to answer the user or continue discovery.

### Important rules

- Tool grants are scoped to the current turn. A tool granted in this turn is not available in the next turn.
- You cannot call a concrete plugin tool without first granting it via `tool_schema`.
- If a plugin fails to start, report the error to the user and suggest alternatives.
- Prefer `tool_list` when you want to see everything a plugin offers. Prefer `tool_search` when you have a specific keyword in mind.
