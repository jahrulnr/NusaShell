# Plugins

A NusaShell plugin is a folder with `manifest.json` + `mcp/`, and optionally
`ui/` for a windowed plugin. `ui/` is optional — omit it for a **headless
MCP-only plugin** (no window, not on the Home launcher grid; managed from the
Plugins view and via agent `mcp_*` tools).

- `manifest.json`: name, version, icon, optional UI entry point, MCP transport, and autostart flag.
- `ui/`: optional — the plugin user interface, loaded as an iframe in the shell.
- `mcp/`: the MCP server that exposes tools, prompts, and resources to the agent. Plugin capability howtos belong here as MCP prompts, not in the shell's removable-agnostic docs corpus.

## UI vs headless

- **UI plugin** (manifest has `ui.entry`): appears on the Home launcher grid and
  opens a window. Managed from Home and the Plugins view.
- **Headless plugin** (no `ui`): never opens a window and never appears on Home.
  Managed from the Plugins view (Start / Stop / Autostart / uninstall) and via
  agent `mcp_*` tools. `autostart: true` is the normal way to start a headless
  plugin at boot since there is no UI to open.

`icon` stays required for both shapes (emoji/text like `N` / `📝` is valid).

## Plugin roots and admission

Packaged/read-only built-ins are loaded from the application `resources/plugins/`
tree. User-installed and in-app-agent-authored plugins live under the writable
`{userData}/plugins/` tree. The agent must scaffold there and call
`mcp_register`; writing a folder alone does not add it to plugin inventory.
Humans may use Add Plugin, and all plugins still use `mcp_enable`/`mcp_disable`
for lifecycle. `mcp_unregister` removes only user plugins; bundled built-ins are
protected.

## Plugin states

- **installed**: registered in the repository but not running.
- **running**: the MCP process has been spawned and its tools are available.
- **crashed**: the process stopped unexpectedly; use `mcp_enable` or the launcher to restart.
- **disabled**: the user has stopped the plugin.

## Lifecycle

The agent can control lifecycle through the `mcp_enable`, `mcp_disable`, and `mcp_list` shell-owned tools. Launcher controls also exist for users.

## Autostart

Set `autostart: true` in `manifest.json` to start the MCP server when the shell boots. The UI is only loaded when the user opens the plugin window (windowed plugins only).

## Communication

- Plugin UI to host: `window.shell.callTool(tool, args)` over `postMessage`.
- Host to backend: WebSocket commands/queries and events.
- Backend to MCP server: stdio (first supported transport) or remote sse/http.

The two plugin sides never talk to each other directly; the shell is the broker.
