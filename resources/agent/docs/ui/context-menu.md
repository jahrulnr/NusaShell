# Context Menu

Floating menu with context-specific actions. Plugin tiles/rows show plugin actions; inputs show edit actions.

**How to open:** Right-click a plugin tile/row, or right-click an input/textarea for edit actions.

## Edit actions

Available when right-clicking inside an input or textarea.

- **Cut** (`[data-action="cut"].ctx-item`):
  - Section: Edit actions
  - Type: menu item
  - Action: Cuts selected text in an input/textarea.

- **Copy** (`[data-action="copy"].ctx-item`):
  - Section: Edit actions
  - Type: menu item
  - Action: Copies selected text in an input/textarea.

- **Paste** (`[data-action="paste"].ctx-item`):
  - Section: Edit actions
  - Type: menu item
  - Action: Pastes clipboard text into an input/textarea.

## Plugin actions

Available when right-clicking a plugin tile or row.

- **Open** (`[data-action="open"].ctx-item`):
  - Section: Plugin actions
  - Type: menu item
  - Action: Opens the plugin window for the selected plugin.

- **Start** (`[data-action="start"].ctx-item`):
  - Section: Plugin actions
  - Type: menu item
  - Action: Starts the selected plugin's MCP process.

- **Force Stop** (`[data-action="stop"].ctx-item`):
  - Section: Plugin actions
  - Type: menu item
  - Action: Stops the selected plugin's MCP process.

- **Restart** (`[data-action="restart"].ctx-item`):
  - Section: Plugin actions
  - Type: menu item
  - Action: Restarts the selected plugin's MCP process.

- **Details** (`[data-action="detail"].ctx-item`):
  - Section: Plugin actions
  - Type: menu item
  - Action: Opens the plugin detail drawer for the selected plugin.

- **Uninstall** (`[data-action="uninstall"].ctx-item`):
  - Section: Plugin actions
  - Type: menu item
  - Action: Removes the selected plugin after a confirmation prompt.
