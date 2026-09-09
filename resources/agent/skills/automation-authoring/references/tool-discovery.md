# Capability and MCP discovery

An automation YAML `uses:` step names a builtin or registered capability. An
`agent:` step that calls an MCP plugin uses the normal runtime discovery flow
inside the headless toolbox. Keep those mechanisms distinct.

## Before authoring a `uses:` step

1. Inspect the live providers with `mcp_list` when the capability is backed by
   an MCP server.
2. Search the capability/tool catalog instead of guessing an ID.
3. Resolve the exact capability in `automation(op="validate")`.
4. Treat `INVALID` as a missing or malformed capability reference, and
   `BLOCKED` as a stopped/disabled provider. Do not hide either by removing a
   required action.

The live audit showed Telegram available, while Kanban was configured but
stopped and no GitHub MCP provider was available. That is an installation
state, not a reason to invent a fake capability.

## MCP inside an agent step

1. Call `mcp_list` and identify the exact plugin ID. If it is stopped, call
   `mcp_enable(id="<plugin-id>")` once only when the user authorizes the
   capability.
2. Call `mcp_search(query="<intent>", server="<plugin-id>")` or `tool_list` to
   obtain the exact `ref` and bare tool name. Search results are compact and
   do not include the full input schema.
3. Call `tool_schema(server="<plugin-id>", tool="<bare-tool-name>")` when
   required fields, enum values, or target identifiers are unclear.
4. If `mcp_list` marks the plugin with a usage contract, call
   `contract_read(id="<plugin-id>")` before the first action when the current
   contract mode requires it.
5. Call `mcp_call(ref="<exact-ref>", arguments_json={...})` with the exact
   returned ref and JSON object.
6. Inspect the result. Retry only a transient, idempotent operation. Never
   claim a message, review, card change, or deployment from an unobserved or
   guessed call.

Good:

```text
mcp_search(query="send Telegram message", server="nusashell.telegram")
tool_schema(server="nusashell.telegram", tool="<returned-bare-name>")
mcp_call(ref="nusashell.telegram:<returned-bare-name>", arguments_json={<schema-valid JSON object>})
```

Bad:

```text
mcp_call(ref="nusashell.telegram:send_message", arguments_json={guessed fields})
mcp_call(ref="mcp__github__create_review", arguments_json={...})
```

The second example uses an invented route and may target the wrong API. A
Telegram tool may have a different name or schema in another installation.

## Side-effect discovery rule

For a read action, verify the resource identity first. For a write action,
show or state the exact target and proposed mutation, require explicit user
approval unless an existing policy already authorizes it, perform one action,
and retain the successful result in the run log. Never include tokens or
secret headers in an `arguments_json` literal.
