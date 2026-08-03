# Getting Started with NusaShell

NusaShell is a desktop-like shell for AI tools. Each plugin bundles a **UI + MCP server**. Install a plugin and it appears in the launcher grid; tap it to open its UI and spawn its MCP server.

## Main surfaces

- **Launcher**: an Android app-drawer style grid of installed plugins. Open it with the launcher shortcut or from the system tray.
- **Plugin window**: each plugin opens in its own window or tab. The plugin UI is an iframe that talks to the shell host over `postMessage`.
- **Agent chat**: the shell-wide assistant. Use it to start plugins, call MCP tools, and ask about product features.

## Common product questions

- [Where NusaShell stores data](data-locations.md) — OS-specific state roots,
  file inventory, plugin locations, and dev/prod differences.
- [Uninstall NusaShell](uninstall.md) — remove the app, remove one plugin, or
  optionally wipe data.
- [Contribute](contribute.md) — clone, prerequisites, development commands, and
  pull-request checks.
- [Build a plugin](build-plugin.md) — choose headed versus headless MCP and use
  the broker bridge correctly.
- The builtin `skill-creator` teaches agents how to author skills with
  progressive disclosure and optional `requirements.mcp` frontmatter.

## Adding a plugin

- **In-app agent:** read the `mcp-creator` skill, scaffold under
  `{userData}/plugins/{folder}/`, call `mcp_register` with confirmation, then
  `mcp_enable` and verify with `mcp_list`.
- **Human:** use Add Plugin / the Plugins view for a local folder or archive.
- **Cursor/repository development:** use the repository `plugins/` tree.

A folder on disk is not installed inventory until it is admitted. Headless
plugins are managed from Plugins and the agent, not the Home grid.

## Talking to the agent

- Ask the agent to start, stop, or restart a plugin.
- Ask about internal documentation using the built-in `docs_search`, `docs_list`, and `docs_read` tools.
- For diagrams in chat (when to use Mermaid, which type): see
  [mermaid-workflow.md](mermaid-workflow.md).
- When you want to call an MCP tool, the agent discovers it through `tool_search`/`tool_list`, loads its schema with `tool_schema`, and then calls it in a following round.
