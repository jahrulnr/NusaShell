# Plugins

The plugins workspace: installed MCP servers and plugin apps in a searchable list with a detail inspector (state, tools, connection, preferences) and a three-source install dialog (catalog, GitHub, ZIP upload).

**How to open:** Click the Plugins item in the left rail, or open #plugins.

## Header

Search, counts, sync state, update check, and actions to add a raw MCP server or open the install dialog.

- **Plugin search** (`#plugins-search`):

- **Installed count** (`#plugins-count`):

- **Filtered count** (`#plugins-filter-count`):

- **Plugins sync state** (`#plugins-sync-state`):

- **Check updates** (`#plugins-check-updates`):

- **Add MCP** (`#plugins-add-mcp`):
  - Type: button
  - Action: Opens the native MCP server editor.

- **Install plugin** (`#plugins-install`):
  - Type: button
  - Action: Opens the plugin installer dialog.

- **Connection filter** (`[data-plugin-filter]`):
  - Type: button group
  - Action: Filters the plugin list by connection state (all, connected, disconnected).

## Installed plugins

The list shows every installed plugin with runtime status; the inspector renders detail, state machine actions, tools, connection info, and autostart/autoupdate preferences. At widths through 720px the toolbar, filter row, and list hide while a plugin detail is open; the controller-mounted Back control returns to the filtered list and restores its scroll position.

- **Installed plugins list** (`#plugins-list`):

- **Back to plugins** (`#plugins-mobile-back`):
  - Type: button
  - Action: Returns to the filtered plugin list while keeping the selected plugin.
  - Notes: Mounted by the plugins controller as the first child of the detail header; visible only at widths through 720px where list and detail show one pane at a time.

- **Plugin inspector** (`#plugins-inspector`):

- **Inspector empty state** (`#plugins-empty`):

- **Plugin detail** (`#plugins-detail`):

- **Plugin icon** (`#plugins-detail-icon`):

- **Plugin type** (`#plugins-detail-type`):

- **Plugin name** (`#plugins-detail-title`):

- **Plugin ID** (`#plugins-detail-id`):

- **State machine** (`#plugins-detail-status`):

- **Tool count** (`#plugins-detail-tools-count`):

- **Plugin transport** (`#plugins-detail-transport`):

- **Open UI** (`#plugins-detail-open-ui`):
  - Type: button
  - Action: Opens the selected MCP + UI plugin through the shared plugin launcher (served at /plugins/<id>/): a separate floating window on desktop, a normal browser tab at widths through 720px. Hidden for MCP-only entries.

- **Start** (`#plugins-detail-start`):
  - Type: button
  - Action: Connects the selected MCP server or plugin and lists its tools.

- **Stop** (`#plugins-detail-stop`):
  - Type: button
  - Action: Drops the cached MCP connection (plugin.stop).

- **Restart** (`#plugins-detail-restart`):
  - Type: button
  - Action: Stops then starts the selected MCP server or plugin.

- **Update** (`#plugins-detail-update`):
  - Type: button
  - Action: Updates a catalog plugin in place to its latest release (plugin.update). Appears only when a newer catalog version is available; the label shows the target version.

- **Edit** (`#plugins-detail-edit`):
  - Type: button
  - Action: Opens the MCP server editor for the selected native MCP entry. Hidden for plugins.

- **Remove** (`#plugins-detail-remove`):
  - Type: button
  - Action: Removes the plugin after confirmation.

- **Tools card title** (`#plugins-tools-title`):

- **Tools count** (`#plugins-tools-heading-count`):

- **Tools list** (`#plugins-tools-list`):
  - Type: list
  - Notes: Lists the tools exposed by the connected MCP server or plugin with name and description.

- **Connection card title** (`#plugins-connection-title`):

- **Connection info** (`#plugins-connection-list`):
  - Type: panel
  - Notes: Manifest and connection fields for the selected plugin.

- **Start automatically** (`#plugins-detail-autostart`):
  - Type: toggle
  - Action: Launches this MCP server or plugin automatically when NusaShell starts (plugin.set_autostart).

- **Auto update row** (`#plugins-autoupdate-row`):
  - Type: section
  - Notes: Preference row shown only for catalog-managed plugins; contains the auto-update switch.

- **Auto update** (`#plugins-detail-autoupdate`):
  - Type: toggle
  - Action: Updates this catalog plugin automatically when a new version is released (plugin.set_autoupdate). Shown only for catalog-managed plugins.

- **Plugin note** (`#plugins-detail-note`):
  - Type: text

- **List title** (`#plugins-index-title`):

- **List count** (`#plugins-index-count`):

## Install dialog

A tabbed dialog installs plugins from the catalog, a GitHub repository (URL/owner-repo with optional subdirectory and ref), or a ZIP upload containing exactly one manifest.json. Errors surface in a banner inside the dialog.

- **Close install dialog** (`#plugins-install-close`):
  - Type: button
  - Action: Closes the plugin installer.

- **Install error banner** (`#plugins-install-error`):
  - Type: status
  - Notes: Inline error shown inside the installer when an install attempt fails; cleared on tab switch or dialog close.

- **Plugin catalog search** (`#plugins-catalog-search`):
  - Type: search
  - Action: Filters the plugin catalog by name, id, or description.

- **Plugin catalog list** (`#plugins-catalog-list`):
  - Type: list

- **GitHub URL** (`#plugins-github-url`):
  - Type: text
  - Notes: Repository URL or owner/repo shorthand.

- **GitHub subdirectory** (`#plugins-github-subdir`):
  - Type: text
  - Notes: Optional subdirectory inside a monorepo.

- **GitHub ref** (`#plugins-github-ref`):
  - Type: text
  - Notes: Optional branch or tag.

- **ZIP file input** (`#plugins-zip-file`):
  - Type: file

- **Selected ZIP file name** (`#plugins-zip-name`):
  - Type: text

- **Confirm install** (`#plugins-install-confirm`):
  - Type: button
  - Action: Confirms the current install source (GitHub or ZIP).
