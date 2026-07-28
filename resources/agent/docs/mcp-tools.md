# MCP Tool Discovery

NusaShell uses a progressive MCP discovery flow.

## Discover tools

1. `mcp_list` shows installed plugins and whether each MCP server is running.
2. Start the plugin with `mcp_enable` if needed.
3. `tool_search` finds tools by name or description, or `tool_list` lists all tools.
4. `tool_schema` loads the full input schema of a tool.
5. The model then calls the tool in a following round using the schema it just loaded.

## Shell-owned meta-tools

The following meta-tools are always available without a running MCP server:

- `mcp_list`
- `mcp_enable` / `mcp_disable`
- `tool_search` / `tool_list` / `tool_schema`
- `mcp_context`
- `docs_search` / `docs_list` / `docs_read`

## When to use docs tools

If the user asks how to use NusaShell, how plugins work, or what a setting means, prefer `docs_search` first. It searches this corpus and returns relevant chunks. Use `docs_read` with the returned `path` and `chunk_id` to read the full text.
