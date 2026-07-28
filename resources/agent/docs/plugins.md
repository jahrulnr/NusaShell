# Plugins

A NusaShell plugin is a folder with three parts:

- `manifest.json`: name, version, icon, entry points, MCP transport, and autostart flag.
- `ui/`: the plugin user interface, loaded as an iframe in the shell.
- `mcp/`: the MCP server that exposes tools, prompts, and resources to the agent.

## Plugin states

- **installed**: registered in the repository but not running.
- **running**: the MCP process has been spawned and its tools are available.
- **crashed**: the process stopped unexpectedly; use `mcp_enable` or the launcher to restart.
- **disabled**: the user has stopped the plugin.

## Lifecycle

The agent can control lifecycle through the `mcp_enable`, `mcp_disable`, and `mcp_list` shell-owned tools. Launcher controls also exist for users.

## Autostart

Set `autostart: true` in `manifest.json` to start the MCP server when the shell boots. The UI is only loaded when the user opens the plugin window.

## Communication

- Plugin UI to host: `window.shell.callTool(tool, args)` over `postMessage`.
- Host to backend: WebSocket commands/queries and events.
- Backend to MCP server: stdio (first supported transport) or remote sse/http.

The two plugin sides never talk to each other directly; the shell is the broker.
