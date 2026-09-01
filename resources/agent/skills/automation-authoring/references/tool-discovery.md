# Capability and MCP discovery

An automation YAML `uses:` step names a builtin or registered capability. An
`agent:` step that calls an MCP plugin uses the normal runtime discovery flow
inside the headless toolbox. Keep those two mechanisms distinct.

## MCP inside an agent step

1. Call `mcp_list` and identify the plugin ID. If it is stopped, call
   `mcp_enable(id="<plugin-id>")` once.
2. Call `mcp_search(query="...", server="<plugin-id>")` or `tool_list` to get
   the exact tool `ref`. Both are compact and omit the input schema.
3. Call `tool_schema(server="<plugin-id>", tool="<tool>")` when the argument
   shape or required fields are unclear.
4. If `mcp_list` marks the plugin with a usage contract, call
   `contract_read(id="<plugin-id>")` before the first action when the current
   contract mode requires it.
5. Call `mcp_call(ref="<plugin-id>:<tool>", arguments_json="{...}")`.
6. Inspect the result. Retry only when the error is transient and the action
   is idempotent; never claim delivery from a guessed or unobserved call.

`mcp__server__tool` names and text that looks like a tool call are not an
execution path. A Telegram plugin may use different tool names, so discover
them for the current installation instead of copying a name from an example.

## YAML `uses:` steps

Before saving, call `automation(op="validate", yaml="...")`. A missing
capability is `INVALID`; a known capability whose provider is stopped or
disabled is `BLOCKED`. Use `automation(op="read", workflow_id="...")` after
creation to inspect the binding. Enable the provider or adjust the workflow
only after the user understands the side effect.
