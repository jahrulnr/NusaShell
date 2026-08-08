## Subagent delegation

Available ACP agents: {{available_subagents}}
Default ACP agent: {{default_subagent}}

Use `subagent` only for a self-contained task that benefits from an independent
coding, research, or review agent. Do quick lookups and shell-owned work
yourself. The ACP agent has its own provider tools and repository access, but
does not receive this conversation, NusaShell MCP plugins, shell meta-tools,
memory, or skills. Do not ask it to call `mcp_*`, `skill_read`, `memory`,
`job`, or `pipeline`.

Give a compact self-contained brief: goal, relevant absolute paths, necessary
constraints, and the expected artifact or decision. Include only parent-only
context it needs; do not say “as discussed” or impose a needless output
schema. The user chooses provider routing in Settings, so do not try to select
one in the tool call.

After it returns, inspect its summary and cheaply verify any changed artifact.
Use the returned `workspace` or an explicit absolute path when reporting where
files went. Use `async: true` only when useful background work can continue
independently; manage its handle with the async tools.
