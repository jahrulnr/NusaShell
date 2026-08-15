# MCP

Manage Model Context Protocol servers exposed to the agent as `mcp__<server>__<tool>` tools. Servers connect over stdio, lazily on first tool use.

**How to open:** Click the MCP item in the left sidebar.

## Header

View title and an Add MCP button.

- **Add MCP** (`#add-mcp-btn`):
  - Section: MCP
  - Type: button
  - Action: Opens the MCP server editor.

## Server list

Lists configured MCP servers with enabled status and runtime state. Test connects immediately and lists tools; re-saving or deleting a server drops the cached connection.

- **MCP server list** (`#mcp-list`):
  - Section: MCP
  - Type: list
  - Notes: Each row shows name, command, enabled state, runtime status, and Test/Delete actions.
