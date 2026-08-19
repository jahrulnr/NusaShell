# In-app plugin contract

A valid staged plugin folder contains:

```text
/absolute/staging/example.plugin/
├── manifest.json
└── mcp/
    └── server.cjs
```

Headed plugins additionally contain the declared `ui.entry` file. `icon` is
required. The MCP manifest must use a supported `stdio`, `http`, or `sse`
transport and declare the exact server artifact it runs. Keep the staging folder
outside the installed plugins directory; `mcp_register` copies it into the final
location and may replace an existing plugin with the same id.

Tools must follow the **create** naming rule: let `domain` = last segment of
the plugin id; tool names must **not** start with `${domain}_` and must **not**
equal `domain`. Prefer short verbs (`list`, `read`, `write`, `exec`) or
multi-word verbs without the domain (`list_projects`, `create_ticket`). The
shell exposes discovered tools as `mcp__<server>__<tool>`, where `<server>` is
the manifest name. **If wrapping an existing MCP catalog, preserve upstream
tool names as-is** (no domain redesign). Tools must strictly validate bounded
inputs, return structured content with safe text fallback, and avoid credentials
in schemas, results, and logs. Put required ordering and constraints in bounded
tool descriptions because the current in-app toolbox does not expose MCP prompt
resources.

Before registration, verify the folder and declared files with Files. Build and
test with Terminal using an absolute `cwd`. After registration, verify:

1. `mcp_list` shows the plugin and its userData install path.
2. `mcp_enable` starts it.
3. `tool_list` or `tool_search` finds the implemented tools.
4. `tool_schema` is loaded before any tool call.
5. One discovered `mcp__<server>__<tool>` call returns the expected result.
