# Agent tools

The agent ships with a built-in toolbox plus one tool per MCP server tool.

## Built-ins

| Tool | Purpose |
| --- | --- |
| `skill_list` | list available skills (name + description) |
| `skill_search` | search installed skills by name or description (case-insensitive substring) |
| `skill_read` | read a skill's full markdown content by name |
| `skill_run` | load a skill's instructions by name (alias of `skill_read` for the legacy prompt path) |
| `memory_save` | persist a fact with optional tags |
| `memory_search` | substring search over memory |
| `memory_list` | list all memory entries |
| `memory_delete` | remove a memory entry by id |
| `todo` | replace the conversation task checklist (full-replace, Claude TodoWrite style; max 50 items, 500 chars each; prefer exactly one `in_progress` at a time). The user can delete items from the UI — treat deleted items as gone and do not re-add them. |
| `docs_search` | search the product documentation |
| `docs_read` | read a documentation page by id |
| `mcp_list` | list configured MCP servers with enabled status and runtime state (running/stopped) |
| `tool_list` | list tools from a running MCP server (or across all running servers when the server is omitted) |
| `tool_search` | search a running MCP server's tools by name or description |
| `tool_schema` | load one MCP tool's input schema by server and tool name before calling it |
| `read_image` | load an image from the conversation into the model's context (vision models see it directly; non-vision models get a text description via the vision fallback) |

The system prompt advertises the same set: `skill_list`, `skill_search`,
`skill_read`, `memory_*`, `docs_*`, `mcp_list`, `tool_list`, `tool_search`,
`tool_schema`, `read_image`, plus `mcp__<server>__<tool>` for each enabled MCP server.

## MCP tools

Every tool of an enabled MCP server is exposed as `mcp__<server>__<tool>`
with the server's own input schema. The shell connects to the server on
first use (stdio) and keeps the connection for the process lifetime;
re-saving or deleting the server drops the connection.
