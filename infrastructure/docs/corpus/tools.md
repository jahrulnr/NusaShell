# Agent tools

The agent ships with a built-in toolbox plus one tool per MCP server tool.

## Built-ins

| Tool | Purpose |
| --- | --- |
| `skill_list` | list available skills (name + description) |
| `skill_run` | load a skill's instructions by name |
| `memory_save` | persist a fact with optional tags |
| `memory_search` | substring search over memory |
| `memory_list` | list all memory entries |
| `memory_delete` | remove a memory entry by id |
| `docs_search` | search the product documentation |
| `docs_read` | read a documentation page by id |

## MCP tools

Every tool of an enabled MCP server is exposed as `mcp__<server>__<tool>`
with the server's own input schema. The shell connects to the server on
first use and keeps the connection for the process lifetime; re-saving or
deleting the server drops the connection.
