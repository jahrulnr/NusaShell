# MCP requirements

Use a structured header whenever the skill needs plugin capability:

```yaml
requirements:
  mcp:
    - nusashell.files
    - role:terminal
```

Concrete ids name a specific installed plugin. `role:files` and `role:terminal`
allow a suitable substitute when the skill only needs that capability role. The
agent must check `mcp_list`, enable a selected plugin with `mcp_enable`, and use
live `tool_list`/`tool_schema` before calling tools. Missing dependencies should
be explained to the user; the current runtime uses a soft prompt gate rather
than refusing `skill_read`.
