# In-app plugin contract

A valid plugin folder contains:

```text
{userData}/plugins/example.plugin/
├── manifest.json
└── mcp/
    ├── server.cjs
    └── prompts.js       # required for domain flows; recommended for native-like tools
```

Headed plugins additionally contain the declared `ui.entry` file. `icon` is
required. The MCP manifest must use a supported `stdio`, `http`, or `sse`
transport and declare the exact server artifact it runs.

Tools must follow the **create** naming rule: let `domain` = last segment of
the plugin id; tool names must **not** start with `${domain}_` and must **not**
equal `domain`. Prefer short verbs (`list`, `read`, `write`, `exec`) or
multi-word verbs without the domain (`list_projects`, `create_ticket`). The
shell expands to `mcp_<pluginId>_<tool>` for agent-facing uniqueness. **If
wrapping an existing MCP catalog, preserve upstream tool names as-is** (no
domain redesign). Tools must strictly validate bounded inputs, return
structured content with safe text fallback, and avoid credentials in
schemas/results/logs. Use `prompts/list` and `prompts/get` for plugin-owned
narrative howtos. Schemas alone are insufficient for ordered domain workflows.

Before registration, verify the folder and declared files with Files. Build and
test with Terminal using an absolute `cwd`. After registration, verify:

1. `mcp_list` shows the plugin and its userData install path.
2. `mcp_enable` starts it.
3. `tool_list` or `tool_search` finds the implemented tools.
4. `mcp_context` `list_prompts` / `get_prompt` reaches the plugin howto.
5. `tool_schema` is loaded before any tool call.
