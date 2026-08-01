# Workspace MCP binding

`conversation.workspace` is the source of truth for agent tool I/O. This
document records how the shell binds it to MCP plugins, in locked order, and
why a hard agent-vs-UI MCP pool is rejected for MVP.

## The gap this closes

Before this work, `conversation.workspace` was **prompt-only**: it flowed into
`{{workspace}}` via `RunAgentTurnHandler.injectSystemPrompts` and never reached
`AgentTurnRunner`, `McpAgentToolGateway`, or `PluginRuntimeManager.callTool`.
The Files root was fixed at MCP process start (`NUSASHELL_FILES_ROOT` or home);
the Terminal `cwd` defaulted to home. The model was told "work in X" while
tools acted relative to home — a prompt-vs-tool-reality mismatch that the
model correctly protested.

This is a known MCP ecosystem gap, not NusaShell-only: workspace in the prompt
does not reach stdio MCP servers that bake root into env/args at spawn.

## Locked binding order

```text
conversation.workspace
  → system prompt (context)
  → gateway arg wrap (Terminal cwd, Files relative paths)
  → MCP Roots + roots/list_changed (no restart when the server supports it)
  → respawn with mcp_enable args/env overrides (static servers)
  → keyed (pluginId, workspaceId) pool — deferred, only if respawn hurts UX
```

### Phase 1 — Gateway arg wrap

`McpAgentToolGateway.callGrantedTool` applies host-side wrapping before
dispatching a granted tool call, using the turn workspace from
`AgentTurnContext.workspace`:

- **Terminal** (`terminal_exec`, `terminal_open`): omitted or relative `cwd`
  becomes the absolute workspace. An explicit absolute `cwd` is preserved.
- **Files**: relative `path`/`source`/`destination` arguments are rewritten to
  absolute paths under the workspace. Absolute paths, `/`, and empty are
  preserved. The Files server still enforces root containment, so an absolute
  path outside the Files root is rejected (the "else need Phase 2/3" fallback).

Wrapping is pure (`workspace-tool-wrap.ts`) and only rewrites *relative*
values, so the model can still target OS-absolute locations when it means to.
Third-party plugins are passed through unchanged.

### Phase 2 — MCP Roots

The shell is an MCP **client** that advertises the `roots` capability with
`listChanged`. `StdioMcpClient` answers `roots/list` with the current workspace
root and sends `roots/list_changed` when the workspace changes
(`PluginRuntimeManager.syncWorkspace`). The bundled Files server calls
`roots/list` on connect and re-fetches on `roots/list_changed`, updating its
in-process root via `FileService.setRoot` — **no process restart**.

A server is treated as roots-capable once it has called `roots/list` at least
once (`McpClientPort.rootsRequested()`). Servers that never call it are
static (Phase 3).

Roots are **interoperability, not security**: servers SHOULD respect root
boundaries, not MUST enforce. Many community servers ignore notifications and
read env/args only at startup. See `docs/RISK.md`.

### Phase 3 — Static-server respawn + enable overrides

For static (non-roots) servers, the workspace is recorded on the runtime entry
and applied on the next spawn via the `NUSASHELL_WORKSPACE` env var (the Files
plugin reads `NUSASHELL_FILES_ROOT` → `NUSASHELL_WORKSPACE` → home). The shell
does **not** auto-respawn on every workspace switch, so workspace-agnostic
plugins (e.g. Terminal, whose `cwd` is per-call) are not needlessly restarted
and do not lose in-memory sessions. To force a rebind, use `mcp_enable` with
launch overrides.

`mcp_enable` accepts optional `args` and `env` overrides:

- The agent sees `command`, `args`, and env **keys** (values redacted) via
  `mcp_list` → `launchSpec`.
- It may patch `args`/`env`; **`command` is immutable**.
- A different launchSpec while the plugin is running triggers a stop+start
  respawn (in-memory state loss is accepted).

## Decision locks

1. Workspace SoT for agent tool I/O = `conversation.workspace` (when set).
2. Preference: **wrap → Roots → respawn/enable overrides → keyed pool (later)**.
3. Default: one live process per plugin when UI and agent share a workspace;
   divergence is handled later via a keyed pool, not hardcoded agent/UI pools.
4. ACP `cwd` remains separate (workspace → `session/new` `cwd`).
5. Launch overrides: args/env mutable, command frozen; residual risk in
   `docs/RISK.md`.
6. Roots = interoperability, not security.

## Rejected for MVP

- Hardcoded separate agent-only vs UI-only MCP pools (worse UX/Status
  divergence than a keyed pool).
- Per-call env mutation on a live stdio child.
- Relying on the model to always pass absolute paths.
- Echoing secret env values in `mcp_list`.
- Changing `command` via `mcp_enable`.
- Treating Roots as enforced sandbox / trusted-model-as-security.
- Keyed `(pluginId, workspaceId)` multi-instance pool until respawn pain is
  proven (Phase 4, deferred).

## Touch points

| Concern | Location |
| --- | --- |
| Turn context carries workspace | `RunAgentTurnHandler` → `beginTurn({ workspace })` → gateway |
| Arg wrap helpers | `packages/application/src/agent/services/workspace-tool-wrap.ts` |
| Gateway wrap + `mcp_enable`/`mcp_list` overrides | `packages/application/src/agent/services/mcp-agent-tool-gateway.ts` |
| Runtime sync + launchSpec + respawn | `packages/application/src/plugin/services/plugin-runtime-manager.ts` |
| MCP client Roots | `packages/infrastructure/src/mcp/stdio-mcp-client.adapter.ts` |
| Files roots consumption + `NUSASHELL_WORKSPACE` | `plugins/files/mcp/{server,config,fs-service}.js` + rebuilt `server.cjs` |
| Prompts | `resources/agent/prompts/{developer,mcp-tools}.md` |
| Risk | `docs/RISK.md` |
