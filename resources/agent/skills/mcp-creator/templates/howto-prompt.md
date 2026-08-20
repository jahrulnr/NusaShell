# Optional upstream MCP how-to prompt

The current NusaShell in-app toolbox does not expose `prompts/list` or
`prompts/get`, so do not make this file a required runtime dependency. Use this
template only when another MCP host explicitly supports prompt resources.

- Purpose: what the plugin is for.
- Main tools: name each tool and when to use it.
- Order: explain any required sequence.
- Constraints: root/cwd, credentials, limits, or containment.
- Failure modes: common errors and the safe recovery.
- Schema reminder: use `tool_list`/`mcp_search`, then `tool_schema`; do not
  invent arguments.
