# Getting Started with NusaShell

NusaShell is a desktop-like shell for AI tools. Each plugin bundles a **UI + MCP server**. Install a plugin and it appears in the launcher grid; tap it to open its UI and spawn its MCP server.

## Main surfaces

- **Launcher**: an Android app-drawer style grid of installed plugins. Open it with the launcher shortcut or from the system tray.
- **Plugin window**: each plugin opens in its own window or tab. The plugin UI is an iframe that talks to the shell host over `postMessage`.
- **Agent chat**: the shell-wide assistant. Use it to start plugins, call MCP tools, and ask about product features.

## Adding a plugin

1. Place a plugin folder under the configured `pluginsRoot`. A valid plugin folder contains `manifest.json`, a `ui/` folder, and a `mcp/` folder (or a remote MCP transport config).
2. Restart the shell or use the launcher to refresh.
3. The plugin icon appears in the launcher.

## Talking to the agent

- Ask the agent to start, stop, or restart a plugin.
- Ask about internal documentation using the built-in `docs_search`, `docs_list`, and `docs_read` tools.
- When you want to call an MCP tool, the agent discovers it through `tool_search`/`tool_list`, loads its schema with `tool_schema`, and then calls it in a following round.
