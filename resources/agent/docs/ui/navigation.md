# Navigation and Shell Chrome

Global chrome surrounding every NusaShell view: title bar, window controls, update banner, sidebar, and connection status.

**How to open:** Always visible when the NusaShell main window is open.

## Update banner

A status banner at the top of the window that reports update availability, download progress, and a restart prompt.

- **Update banner** (`#update-banner`):
  - Section: Update banner
  - Type: banner
  - Action: Displays update availability and download progress. Hidden when no update is pending.
  - Related: Update banner text (`#update-banner-text`), Restart to update (`#update-banner-btn`), Close update banner (`#update-banner-close`)

- **Update banner text** (`#update-banner-text`):
  - Section: Update banner
  - Type: status text
  - Action: Shows the current update message, e.g. 'Update available: vX.Y.Z' or download percentage.

- **Restart to update** (`#update-banner-btn`):
  - Section: Update banner
  - Type: button
  - Action: Quits and installs the downloaded update after confirmation. Only shown when an update is ready.

- **Close update banner** (`#update-banner-close`):
  - Section: Update banner
  - Type: button
  - Action: Dismisses the update banner until the next update event.

## Title bar

The top bar contains the NusaShell brand, the global app search, a settings shortcut, and native window controls.

- **Global app search** (`#search-input`):
  - Section: Title bar
  - Type: search input
  - Action: Filters the Home app grid by plugin name, plugin id, or description as the user types. Also updates Plugins and other views that honor the query.
  - Shortcut: Escape clears the current query when the input is focused.
  - Related: Clear search (`#search-clear`), Installed plugin grid (`#app-grid`)

- **Clear search** (`#search-clear`):
  - Section: Title bar
  - Type: button
  - Action: Clears the global app search field and refocuses it.
  - Related: Global app search (`#search-input`)

- **Settings** (`#nav-settings-btn`):
  - Section: Title bar
  - Type: icon button
  - Action: Switches to the Settings view from anywhere in the app.
  - Related: settings (`#settings`)

- **Minimize window** (`#window-minimize`):
  - Section: Title bar
  - Type: window button
  - Action: Minimizes the NusaShell window.

- **Maximize / restore window** (`#window-maximize`):
  - Section: Title bar
  - Type: window button
  - Action: Toggles the NusaShell window between normal and maximized state.

- **Close window** (`#window-close`):
  - Section: Title bar
  - Type: window button
  - Action: Closes the NusaShell window. The backend may continue running in the tray.

## Sidebar

Vertical navigation on the left. Each item switches to a top-level view. The connection meter sits at the bottom.

- **Home navigation** (`[data-view="home"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Home (launcher) view.
  - Related: Installed plugin grid (`#app-grid`), Global app search (`#search-input`)

- **Plugins navigation** (`[data-view="plugins"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Plugins list view.
  - Related: Installed plugin table (`#plugin-table`)

- **Agent navigation** (`[data-view="agent"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Agent conversation view.
  - Related: Conversation thread (`#agent-thread`), Message input (`#agent-input`)

- **AI Providers navigation** (`[data-view="ai-providers"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the AI Providers registry view.
  - Related: Provider registry (`#provider-registry`)

- **Autostart navigation** (`[data-view="autostart"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Autostart view.
  - Related: Autostart list (`#autostart-list`)

- **Logs navigation** (`[data-view="logs"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Logs tail view.
  - Related: Log tail (`#log-tail`)

- **Add Plugin** (`#open-add-plugin`):
  - Section: Sidebar
  - Type: button
  - Action: Opens the Add Plugin modal to install a plugin from a URL or local path.
  - Related: Add Plugin modal (`#add-plugin-modal`), Install from URL (`#install-url-input`)

- **Connection status text** (`#conn-status`):
  - Section: Sidebar
  - Type: status text
  - Action: Displays 'Connecting...' or 'Connected' depending on the WebSocket state.

- **Connection status bar** (`#conn-fill`):
  - Section: Sidebar
  - Type: progress bar
  - Action: Visual fill showing WebSocket connection progress.
