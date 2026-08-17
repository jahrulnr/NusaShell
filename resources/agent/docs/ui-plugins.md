# Plugins

Manage all plugins: manual MCP servers, MCP-only plugins, and MCP + UI plugins appear in one catalog.

**How to open:** Click the Plugins item in the left sidebar.

## Header

View title and two buttons: Add MCP opens the stdio server editor; Install plugin opens the plugin installer with catalog, GitHub, and ZIP options.

- **Add MCP** (`#plugins-add-mcp`):
  - Section: Plugins
  - Type: button
  - Action: Opens the native MCP server editor.

- **Install plugin** (`#plugins-install-plugin`):
  - Section: Plugins
  - Type: button
  - Action: Opens the plugin installer dialog.

## Plugin installer

A tabbed dialog for installing plugins. Catalog shows official first-party plugins. GitHub installs from a repository URL, optionally targeting a subdirectory for monorepos. Upload ZIP lets the user browse or drop a plugin archive.

- **Plugin install dialog** (`#plugin-install-overlay`):
  - Section: Plugins
  - Type: dialog

- **Install plugin title** (`#plugin-install-title`):
  - Section: Plugins
  - Type: heading

- **Close install dialog** (`#plugin-install-close`):
  - Section: Plugins
  - Type: button
  - Action: Closes the plugin installer.

- **Cancel install** (`#plugin-install-cancel`):
  - Section: Plugins
  - Type: button
  - Action: Closes the plugin installer without installing.

- **Confirm install** (`#plugin-install-confirm`):
  - Section: Plugins
  - Type: button
  - Action: Confirms the current install source (GitHub or ZIP).

- **Catalog tab** (`#plugin-install-tab-catalog`):
  - Section: Plugins
  - Type: tab

- **GitHub tab** (`#plugin-install-tab-github`):
  - Section: Plugins
  - Type: tab

- **Upload ZIP tab** (`#plugin-install-tab-zip`):
  - Section: Plugins
  - Type: tab

- **Catalog panel** (`#plugin-install-panel-catalog`):
  - Section: Plugins
  - Type: panel

- **GitHub panel** (`#plugin-install-panel-github`):
  - Section: Plugins
  - Type: panel

- **Upload ZIP panel** (`#plugin-install-panel-zip`):
  - Section: Plugins
  - Type: panel

- **Install error banner** (`#plugin-install-error`):
  - Section: Plugins
  - Type: status
  - Notes: Inline error shown inside the installer when an install attempt fails; cleared on tab switch or dialog close.

- **Plugin catalog search** (`#plugin-catalog-search`):
  - Section: Plugins
  - Type: search
  - Action: Filters the plugin catalog by name, id, or description.

- **Plugin catalog list** (`#plugin-catalog-list`):
  - Section: Plugins
  - Type: list

- **GitHub URL** (`#plugin-install-github-url`):
  - Section: Plugins
  - Type: text
  - Notes: Repository URL or owner/repo shorthand.

- **GitHub subdirectory** (`#plugin-install-github-subdir`):
  - Section: Plugins
  - Type: text
  - Notes: Optional subdirectory inside a monorepo.

- **GitHub ref** (`#plugin-install-github-ref`):
  - Section: Plugins
  - Type: text
  - Notes: Optional branch or tag.

- **ZIP file input** (`#plugin-install-zip-file`):
  - Section: Plugins
  - Type: file

- **Selected ZIP file name** (`#plugin-install-zip-name`):
  - Section: Plugins
  - Type: text

## Plugin window

An in-shell window that renders plugin UIs in a movable, resizable frame sized from the manifest ui.window (fullscreen mode covers the shell; panel/widget use defaultSize with a 700x700 / 380x280 fallback). Opens from Home tiles and the Plugins drawer Open UI button; no browser popups. Close with the X button or Escape.

- **Plugin window** (`#plugin-window`):
  - Section: Plugins
  - Type: dialog
  - Notes: In-shell window hosting the plugin UI in an iframe; sized per manifest ui.window (fullscreen / panel / widget), movable via the title bar, resizable when allowed.

- **Plugin window title** (`#plugin-window-title`):
  - Section: Plugins
  - Type: text

- **Close plugin window** (`#plugin-window-close`):
  - Section: Plugins
  - Type: button
  - Action: Closes the in-shell plugin window (Escape also works).

- **Plugin window frame** (`#plugin-window-frame`):
  - Section: Plugins
  - Type: frame
  - Notes: Iframe hosting /plugins/{id}/ with the window.shell shim.

## Plugin catalog

Lists all plugins — manual MCP servers, MCP-only plugins, and MCP + UI plugins — with runtime state (idle/connected). Clicking a row opens the Plugins detail drawer.

- **Plugin catalog** (`#plugin-table`):
  - Section: Plugins
  - Type: list
  - Notes: Lists all plugins with runtime status and tool chips. Clicking a row opens the Plugins detail drawer.

## Detail drawer

A right-hand drawer that opens when a catalog row is clicked. Shows the state machine, actions, preferences, tools, and manifest summary. Native MCP rows expose Start, Stop, Restart, Edit, and Delete; plugin rows expose Start, Stop, Restart, and Uninstall; MCP + UI plugins also expose Open UI. When a catalog plugin has a newer release, an Update button appears that updates it in place (plugin.update). Close with the X button, overlay, or Escape.

The Preferences area has an Auto start switch (shown for every entry) that launches the MCP server when NusaShell starts, and an Auto update switch (shown only for catalog-managed plugins) that updates the plugin automatically when a new version is released.

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
  - Action: Opens the selected MCP + UI plugin in the in-shell plugin window. Hidden for MCP-only entries.

- **Start** (`#plugin-btn-start`):
  - Section: Plugins
  - Type: button
  - Action: Connects the selected MCP server or plugin and lists its tools.

- **Stop** (`#plugin-btn-stop`):
  - Section: Plugins
  - Type: button
  - Action: Drops the cached MCP connection (plugin.stop).

- **Restart** (`#plugin-btn-restart`):
  - Section: Plugins
  - Type: button
  - Action: Stops then starts the selected MCP server or plugin.

- **Update** (`#plugin-btn-update`):
  - Section: Plugins
  - Type: button
  - Action: Updates a catalog plugin in place to its latest release (plugin.update). Appears only when a newer catalog version is available; the label shows the target version.

- **Edit** (`#plugin-btn-edit`):
  - Section: Plugins
  - Type: button
  - Action: Opens the MCP server editor for the selected native MCP entry. Hidden for plugins.

- **Delete** (`#plugin-btn-delete`):
  - Section: Plugins
  - Type: button
  - Action: Deletes the selected manual MCP-server plugin (plugin.delete). Hidden for installed plugins.

- **Uninstall** (`#plugin-btn-uninstall`):
  - Section: Plugins
  - Type: button
  - Action: Uninstalls the selected plugin and drops its MCP connection (plugin.uninstall). Hidden for native MCP entries.

- **Auto start section** (`#plugin-drawer-autostart`):
  - Section: Plugins
  - Type: section
  - Notes: Preference row shown for every entry; contains the auto-start switch.

- **Auto start** (`#plugin-autostart-toggle`):
  - Section: Plugins
  - Type: toggle
  - Action: Launches this MCP server or plugin automatically when NusaShell starts (plugin.set_autostart).

- **Auto update section** (`#plugin-drawer-autoupdate`):
  - Section: Plugins
  - Type: section
  - Notes: Preference row shown only for catalog-managed plugins; contains the auto-update switch.

- **Auto update** (`#plugin-autoupdate-toggle`):
  - Section: Plugins
  - Type: toggle
  - Action: Updates this catalog plugin automatically when a new version is released (plugin.set_autoupdate). Shown only for catalog-managed plugins.

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
