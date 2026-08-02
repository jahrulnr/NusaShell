# Plugins

List of all installed plugins (UI and headless MCP-only) with their live MCP process state in a compact table. Headless plugins that do not appear on Home are managed here.

**How to open:** Click the Plugins item in the left sidebar.

## Plugin table

Shows plugin icon, name, plugin id, version, and current state for every installed plugin, including headless MCP-only plugins that do not appear on Home. Click any row to open the plugin detail drawer.

- **Installed plugin table** (`#plugin-table`):
  - Section: Plugin table
  - Type: list/table
  - Action: Container for plugin rows. Renders an empty message when no plugins are installed.
  - Related: Plugin row (`.plugin-row`)

- **Plugin row** (`.plugin-row`):
  - Section: Plugin table
  - Type: button
  - Action: Click to open the plugin detail drawer. Shows icon, name, plugin id, version, and state.
  - Related: plugin-detail (`#plugin-detail`)

- **Add MCP** (`#plugins-add-mcp`):
  - Section: Plugins toolbar
  - Type: button
  - Action: Opens the Custom MCP tab for creating a native headless MCP wrapper.

- **Install plugin** (`#plugins-install-plugin`):
  - Section: Plugins toolbar
  - Type: button
  - Action: Opens the NusaShell Plugin tab for installing a packaged plugin.
