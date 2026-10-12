# Plugins

Manage all plugins: manual MCP servers, MCP-only plugins, and MCP + UI plugins appear in one catalog.

**How to open:** Click the Plugins item in the left sidebar.

## Header

View title and two buttons: Add MCP opens the MCP server editor (stdio child process, SSE, or Streamable HTTP remote endpoint); Install plugin opens the plugin installer with catalog, GitHub, and ZIP options.

- **Add MCP** (`#plugins-add-mcp`):
  - Type: button
  - Action: Opens the native MCP server editor.

- **Install plugin** (`#plugins-install-plugin`):
  - Type: button
  - Action: Opens the plugin installer dialog.

## Plugin installer

A tabbed dialog for installing plugins. Catalog shows official first-party plugins. GitHub installs from a repository URL, optionally targeting a subdirectory for monorepos. Upload ZIP lets the user browse or drop a plugin archive.

- **Close install dialog** (`#plugin-install-close`):
  - Type: button
  - Action: Closes the plugin installer.

- **Cancel install** (`#plugin-install-cancel`):
  - Type: button
  - Action: Closes the plugin installer without installing.

- **Confirm install** (`#plugin-install-confirm`):
  - Type: button
  - Action: Confirms the current install source (GitHub or ZIP).

- **Install error banner** (`#plugin-install-error`):
  - Type: status
  - Notes: Inline error shown inside the installer when an install attempt fails; cleared on tab switch or dialog close.

- **Plugin catalog search** (`#plugin-catalog-search`):
  - Type: search
  - Action: Filters the plugin catalog by name, id, or description.

- **Plugin catalog list** (`#plugin-catalog-list`):
  - Type: list

- **GitHub URL** (`#plugin-install-github-url`):
  - Type: text
  - Notes: Repository URL or owner/repo shorthand.

- **GitHub subdirectory** (`#plugin-install-github-subdir`):
  - Type: text
  - Notes: Optional subdirectory inside a monorepo.

- **GitHub ref** (`#plugin-install-github-ref`):
  - Type: text
  - Notes: Optional branch or tag.

- **ZIP file input** (`#plugin-install-zip-file`):
  - Type: file

- **Selected ZIP file name** (`#plugin-install-zip-name`):
  - Type: text

## Plugin window

Plugin UIs open in a separate browser window served same-origin at /plugins/<id>/. Opening happens from Home tiles and the Plugins drawer Open UI button; a stable per-plugin window target means a second click focuses the existing window instead of stacking a duplicate. The window is sized from the plugin manifest (a panel with no declared size falls back to a screen-scaled min(1100px, 80% of width) by min(820px, 85% of height), a widget falls back to 380x280, and fullscreen covers the available screen). This keeps the plugin from covering or replacing the NusaShell UI. If the browser blocks the pop-up, a toast prompts the user to allow pop-ups.

## Plugin catalog

Lists all plugins — manual MCP servers, MCP-only plugins, and MCP + UI plugins — with runtime state (idle/connected). Clicking a row opens the Plugins detail drawer.

- **Plugin catalog** (`#plugin-table`):
  - Type: list
  - Notes: Lists all plugins with runtime status and tool chips. Clicking a row opens the Plugins detail drawer.

## Detail drawer

A right-hand drawer that opens when a catalog row is clicked. Shows the state machine, actions, preferences, tools, and manifest summary. Native MCP rows expose Start, Stop, Restart, Edit, and Delete; plugin rows expose Start, Stop, Restart, and Uninstall; MCP + UI plugins also expose Open UI. When a catalog plugin has a newer release, an Update button appears that updates it in place. Close with the X button, overlay, or Escape. The manifest summary shows transport plus URL for SSE/HTTP servers, or command plus arguments for stdio servers. The same editor (opened via Add MCP or Edit) switches its fields per transport: command/args/env for stdio, URL/headers for SSE/HTTP.

The Preferences area has an Auto start switch (shown for every entry) that launches the MCP server when NusaShell starts, and an Auto update switch (shown only for catalog-managed plugins) that updates the plugin automatically when a new version is released.

- **Plugin detail drawer** (`#plugin-drawer`):
  - Type: drawer
  - Notes: Right-hand panel showing state machine, actions, tools, and manifest for the selected catalog item.

- **Drawer overlay** (`#plugin-drawer-overlay`):
  - Type: overlay
  - Action: Clicking the overlay closes the drawer.

- **Close drawer** (`#plugin-drawer-close`):
  - Type: button
  - Action: Closes the Plugins detail drawer.

- **State machine** (`#plugin-state-machine`):
  - Type: indicator
  - Notes: Shows idle → connecting → connected → error with the current state highlighted.

- **Open UI** (`#plugin-btn-open-ui`):
  - Type: button
  - Action: Opens the selected MCP + UI plugin in a separate browser window (served at /plugins/<id>/). Hidden for MCP-only entries.

- **Start** (`#plugin-btn-start`):
  - Type: button
  - Action: Connects the selected MCP server or plugin and lists its tools.

- **Stop** (`#plugin-btn-stop`):
  - Type: button
  - Action: Drops the cached MCP connection.

- **Restart** (`#plugin-btn-restart`):
  - Type: button
  - Action: Stops then starts the selected MCP server or plugin.

- **Update** (`#plugin-btn-update`):
  - Type: button
  - Action: Updates a catalog plugin in place to its latest release. Appears only when a newer catalog version is available; the label shows the target version.

- **Edit** (`#plugin-btn-edit`):
  - Type: button
  - Action: Opens the MCP server editor for the selected native MCP entry. Hidden for plugins.

- **Delete** (`#plugin-btn-delete`):
  - Type: button
  - Action: Deletes the selected manual MCP-server plugin. Hidden for installed plugins.

- **Uninstall** (`#plugin-btn-uninstall`):
  - Type: button
  - Action: Uninstalls the selected plugin and drops its MCP connection. Hidden for native MCP entries.

- **Auto start section** (`#plugin-drawer-autostart`):
  - Type: section
  - Notes: Preference row shown for every entry; contains the auto-start switch.

- **Auto start** (`#plugin-autostart-toggle`):
  - Type: toggle
  - Action: Launches this MCP server or plugin automatically when NusaShell starts.

- **Auto update section** (`#plugin-drawer-autoupdate`):
  - Type: section
  - Notes: Preference row shown only for catalog-managed plugins; contains the auto-update switch.

- **Auto update** (`#plugin-autoupdate-toggle`):
  - Type: toggle
  - Action: Updates this catalog plugin automatically when a new version is released. Shown only for catalog-managed plugins.

- **Tools list** (`#plugin-tools-list`):
  - Type: list
  - Notes: Lists the tools exposed by the connected MCP server or plugin with name and description.

- **Tool count** (`#plugin-tool-count`):
  - Type: indicator
  - Notes: Number of tools currently loaded for the selected entry.

- **Manifest info** (`#plugin-manifest-info`):
  - Type: panel
  - Notes: Key/value summary of the selected MCP server or plugin, including version, category, and install path when available.
