# Plugins

Manage native Model Context Protocol servers and installed plugins. Both MCP-only plugins and plugins that expose a browser UI appear in one catalog.

**How to open:** Click the Plugins item in the left sidebar.

## Header

View title and an Add MCP button that opens the stdio server editor.

- **Add MCP** (`#plugins-add-mcp`):
  - Section: Plugins
  - Type: button
  - Action: Opens the native MCP server editor.

## Plugin catalog

Lists native MCP servers, headless MCP plugins, and MCP + UI plugins with runtime state. Clicking a row opens the Plugins detail drawer.

- **Plugin catalog** (`#plugin-table`):
  - Section: Plugins
  - Type: list
  - Notes: Lists native MCP servers, MCP-only plugins, and MCP + UI plugins with runtime status and tool chips. Clicking a row opens the Plugins detail drawer.

## Detail drawer

A right-hand drawer that opens when a catalog row is clicked. Shows the state machine, actions, tools, and manifest summary. Native MCP rows expose Test, Stop, Restart, Edit, and Delete; plugin rows expose Test, Stop, Restart, and Uninstall; MCP + UI plugins also expose Open UI. Close with the X button, overlay, or Escape.

- **Plugin detail drawer** (`#plugin-drawer`):
  - Section: Plugins
  - Type: drawer
  - Notes: Right-hand panel showing state machine, actions, tools, and manifest for the selected catalog item.

- **Drawer overlay** (`#plugin-drawer-overlay`):
  - Section: Plugins
  - Type: overlay
  - Action: Clicking the overlay closes the drawer.

- **Close drawer** (`#plugin-drawer-close`):
  - Section: Plugins
  - Type: button
  - Action: Closes the Plugins detail drawer.

- **State machine** (`#plugin-state-machine`):
  - Section: Plugins
  - Type: indicator
  - Notes: Shows idle → connecting → connected → error with the current state highlighted.

- **Open UI** (`#plugin-btn-open-ui`):
  - Section: Plugins
  - Type: button
  - Action: Opens the selected MCP + UI plugin in a separate browser window. Hidden for MCP-only entries.

- **Test** (`#plugin-btn-test`):
  - Section: Plugins
  - Type: button
  - Action: Connects the selected MCP server or plugin and lists its tools.

- **Stop** (`#plugin-btn-stop`):
  - Section: Plugins
  - Type: button
  - Action: Drops the cached MCP connection (mcp.servers.stop).

- **Restart** (`#plugin-btn-restart`):
  - Section: Plugins
  - Type: button
  - Action: Stops then tests the selected MCP server or plugin.

- **Edit** (`#plugin-btn-edit`):
  - Section: Plugins
  - Type: button
  - Action: Opens the MCP server editor for the selected native MCP entry. Hidden for plugins.

- **Delete** (`#plugin-btn-delete`):
  - Section: Plugins
  - Type: button
  - Action: Deletes the selected native MCP server (mcp.servers.delete). Hidden for plugins.

- **Uninstall** (`#plugin-btn-uninstall`):
  - Section: Plugins
  - Type: button
  - Action: Uninstalls the selected plugin and drops its MCP connection (plugin.uninstall). Hidden for native MCP entries.

- **Tools list** (`#plugin-tools-list`):
  - Section: Plugins
  - Type: list
  - Notes: Lists the tools exposed by the connected MCP server or plugin with name and description.

- **Tool count** (`#plugin-tool-count`):
  - Section: Plugins
  - Type: indicator
  - Notes: Number of tools currently loaded for the selected entry.

- **Manifest info** (`#plugin-manifest-info`):
  - Section: Plugins
  - Type: panel
  - Notes: Key/value summary of the selected MCP server or plugin, including version, category, and install path when available.
