# Plugin Detail Drawer

A right-side drawer that shows details for the selected plugin and lets you start, stop, restart, or uninstall it.

**How to open:** Click a plugin tile on the Home view or a row on the Plugins view, or choose Details from the context menu.

## Header

Shows the plugin icon, name, and plugin id with version. The close button dismisses the drawer.

- **Plugin icon** (`#drawer-icon`):
  - Section: Header
  - Type: icon
  - Action: Shows the plugin icon in the detail drawer.

- **Plugin name** (`#drawer-title`):
  - Section: Header
  - Type: text
  - Action: Displays the selected plugin name.

- **Plugin id and version** (`#drawer-subtitle`):
  - Section: Header
  - Type: text
  - Action: Displays the selected plugin id and version.

- **Close drawer** (`#drawer-close`):
  - Section: Header
  - Type: icon button
  - Action: Closes the plugin detail drawer.

- **Drawer overlay** (`#drawer-overlay`):
  - Section: Header
  - Type: overlay
  - Action: Clicking the dark backdrop closes the plugin detail drawer.

## State

Visual state machine that highlights idle, starting, running, stopping, and crashed states.

- **State machine** (`#state-machine`):
  - Section: State
  - Type: visual indicator
  - Action: Highlights the plugin's lifecycle state: idle, starting, running, stopping, or crashed.

## Actions

Primary lifecycle actions for the selected plugin.

- **Start** (`#btn-start`):
  - Section: Actions
  - Type: button
  - Action: Starts the selected plugin's MCP process.

- **Stop** (`#btn-stop`):
  - Section: Actions
  - Type: button
  - Action: Stops the selected plugin's MCP process.

- **Restart** (`#btn-restart`):
  - Section: Actions
  - Type: button
  - Action: Restarts the selected plugin's MCP process.

- **Uninstall** (`#btn-uninstall`):
  - Section: Actions
  - Type: button
  - Action: Removes the selected plugin after a confirmation prompt.
  - Related: Installed plugin table (`#plugin-table`), Installed plugin grid (`#app-grid`)

## Tools

Lists the tools reported by the plugin's MCP server when running. If the plugin is idle, it prompts to start it so tools can be discovered.

- **Tool count** (`#tool-count`):
  - Section: Tools
  - Type: text
  - Action: Shows the number of tools discovered from the plugin's MCP server.

- **Tools list** (`#tools-list`):
  - Section: Tools
  - Type: list
  - Action: Lists tool names reported by the running plugin. Empty when the plugin is idle.

## Manifest

Displays the plugin manifest (name, version, description, permissions, etc.).

- **Plugin manifest** (`#manifest-info`):
  - Section: Manifest
  - Type: list/description
  - Action: Displays the plugin manifest including name, version, description, and permissions.
