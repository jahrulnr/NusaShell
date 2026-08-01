# MCP Tool Discovery

NusaShell uses a progressive MCP discovery flow.

## Discover tools

1. `mcp_list` shows installed plugins and whether each MCP server is running.
2. Start the plugin with `mcp_enable` if needed.
3. `tool_search` finds tools by name or description, or `tool_list` lists all tools.
4. `tool_schema` loads the full input schema of one tool; `tool_schemas` grants several tools from the same plugin in one call (pass `pluginId` and `toolNames[]`). Prefer batch when you know you need more than one tool.
5. The model then calls the tool in a following round using the schema it just loaded.

## Paths and workspace

The conversation workspace is the source of truth for agent tool I/O (not prompt-only). The shell binds it to bundled path/cwd-shaped tools: Terminal defaults `cwd` to the workspace when omitted; Files rewrites relative `path`/`source`/`destination` against the workspace. Absolute paths are preserved. MCP Roots + `roots/list_changed` update the Files root in-process when supported; static servers may get `NUSASHELL_WORKSPACE` at spawn / via `mcp_enable` overrides. Third-party plugins that ignore roots still need an explicit absolute path. Full design: `docs/architecture/workspace-mcp-binding.md`.

**Files plugin root:** Files containment is under the Files root (`NUSASHELL_FILES_ROOT`, else workspace via roots/`NUSASHELL_WORKSPACE`, else home). `/` or empty means that root — not the OS filesystem root.

## Shell-owned meta-tools

The following meta-tools are always available without a running MCP server:

- `mcp_list`
- `mcp_enable` / `mcp_disable`
- `tool_search` / `tool_list` / `tool_schema` / `tool_schemas`
- `mcp_context`
- `docs_search` / `docs_list` / `docs_read`
- `ask_question` — interactive clarifying question (desktop turns only). Pass a question plus 1-8 options; optionally allow free text or multi-select. The turn pauses until the user answers or cancels.

## When to use docs tools

If the user asks how to use NusaShell, how plugins work, or what a setting means, prefer `docs_search` first. It searches this corpus and returns relevant chunks. Use `docs_read` with the returned `path` and `chunk_id` to read the full text.
