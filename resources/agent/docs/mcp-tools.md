# MCP Tool Discovery

NusaShell uses a progressive MCP discovery flow for **starting** plugins and
**overflow/failure recovery**. When a plugin is already **running**, its full
tool catalog (name + description + `inputSchema`) is injected into the Live MCP
system block and auto-advertised in the provider `tools[]` array every turn —
call `mcp_<plugin>_<tool>` names directly without `tool_schema` first.

## Discover tools

**Running plugins are fully catalogued.** When a plugin is running, you do not
need to discover its tools — they are already in the Live MCP block and in
`tools[]`. The flow below is for idle plugins and recovery.

1. `mcp_list` shows installed plugins and whether each MCP server is running.
2. Start the plugin with `mcp_enable` if needed. The full catalog appears on the next turn or after the next `listTools` refresh.
3. `tool_search` finds tools by name or description, or `tool_list` lists all tools. `tool_search` matches when **any** whitespace-separated keyword in the query hits the tool name or description (case-insensitive) — "read file list directory terminal" matches `read`, `list`, `exec`. Both return an envelope (`{ pluginId, count, tools }` / `{ pluginId, query, matchMode, count, matches, hint? }`). `count: 0` is a valid success (no tools matched), not a turn interrupt or failure. On zero hits, try a shorter one-keyword query, `tool_list` the same plugin, or `mcp_list` if the plugin may be wrong or not running.
4. Prefer `tool_schema` / `tool_schemas` when you need the input schema (first use or unfamiliar args). Pass `pluginId` and `toolName` / `toolNames[]`. Prefer batch when you know you need more than one tool. This step is optional when recalling a known tool on a running plugin (it is already auto-advertised).
5. Call the tool. Provider names look like `mcp_<plugin>_<tool>`. If you already used that tool earlier and the plugin is still running, you may call the name directly without re-running `tool_schema`; the shell resolves it against the running plugin. If the plugin is stopped or the name is wrong, fall back to discovery.

Do not re-discover or re-grant schemas at the start of every turn by habit. Running plugins are already fully catalogued.

For a plugin's narrative howto, use `mcp_context` with `list_prompts` and then
`get_prompt`. Prompt retrieval provides context only; it never executes a tool.
For skill authoring, read the builtin `skill-creator` skill before calling
`skill_manage`. If a skill declares `requirements.mcp`, check `mcp_list` and
enable a concrete plugin or suitable role substitute before following it.
Plugin prompts are owned by the plugin, so uninstalling the plugin removes that
knowledge path. Do not add built-in plugin tool catalogs or plugin-specific
howtos to the shell documentation corpus.

## Paths and workspace

The conversation workspace is the source of truth for agent tool I/O (not prompt-only). The shell binds it to bundled path/cwd-shaped tools: Terminal defaults `cwd` to the workspace when omitted; Files rewrites relative `path`/`source`/`destination` against the workspace. Absolute paths are preserved. MCP Roots + `roots/list_changed` update the Files root in-process when supported; static servers may get `NUSASHELL_WORKSPACE` at spawn / via `mcp_enable` overrides. Third-party plugins that ignore roots still need an explicit absolute path. Full design: `docs/architecture/workspace-mcp-binding.md`.

**Files plugin root:** Files containment is under the Files root (`NUSASHELL_FILES_ROOT`, else workspace via roots/`NUSASHELL_WORKSPACE`, else home). `/` or empty means that root — not the OS filesystem root.

## Shell-owned meta-tools

The following meta-tools are always available without a running MCP server:

- `mcp_list`
- `mcp_enable` / `mcp_disable`
- `mcp_register` / `mcp_unregister` — admit or remove user plugins only under the writable userData `plugins/` root; both require interactive confirmation, reject repo/bundled paths, and are denied to jobs/background turns.
- `tool_search` / `tool_list` / `tool_schema` / `tool_schemas`
- `mcp_context`
- `docs_search` / `docs_list` / `docs_read`
- `ask_question` — interactive clarifying question (desktop turns only). Pass a question plus 1-8 options; optionally allow free text or multi-select. The turn pauses until the user answers or cancels.

## When to use docs tools

If the user asks how to use NusaShell, how plugins work, or what a setting means, prefer a known static path with `docs_read` when you already know it (for example `jobs-howto.md`, `pipelines-howto.md`, `data-locations.md`, `build-plugin.md`, `mermaid-workflow.md`). Use `docs_search` only when the path is unknown; then `docs_read` with the returned `path` and `chunk_id`.

Plugin capability catalogs are not in this corpus — use live `mcp_list` → `tool_list` / `mcp_context` instead.
