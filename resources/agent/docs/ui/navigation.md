# Navigation and Shell Chrome

Global chrome surrounding every NusaShell view: title bar, window controls, update banner, two-mode sidebar, documentation link, and connection status.

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

The top bar identifies NusaShell with its tile-and-wave brand mark and wordmark, followed by compact backend connection status, settings and always-on-top shortcuts, and native window controls. App search is intentionally scoped to Home.

- **Connection status text** (`#conn-status`):
  - Section: Title bar
  - Type: status text
  - Action: Displays 'Connecting...' or 'Connected' depending on the WebSocket state.

- **Connection status indicator** (`#conn-fill`):
  - Section: Title bar
  - Type: status indicator
  - Action: Changes from neutral to green when the backend WebSocket is connected.

- **Settings** (`#nav-settings-btn`):
  - Section: Title bar
  - Type: icon button
  - Action: Switches to the Settings view from anywhere in the app.
  - Related: settings (`#settings`)

- **Keep window on top** (`#window-always-on-top`):
  - Section: Title bar
  - Type: toggle icon button
  - Action: Toggles whether the NusaShell launcher stays above other desktop windows for the current app session.

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

Vertical navigation on the left, ordered Home, Agent, Skills, Plugins, AI Providers, Autostart, and Logs. It can show icons with labels or icons only, remembers that choice locally, and links to the project documentation on GitHub from its footer.

- **Home navigation** (`[data-view="home"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Home (launcher) view.
  - Related: Installed plugin grid (`#app-grid`), Installed app search (`#search-input`)

- **Agent navigation** (`[data-view="agent"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Agent conversation view.
  - Related: Conversation thread (`#agent-thread`), Message input (`#agent-input`)

- **Skills navigation** (`[data-view="skills"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the managed Skills workspace.
  - Related: Installed skills (`#skills-list`), Skill file tree (`#skills-file-tree`), Skill text editor (`#skill-editor`)

- **Plugins navigation** (`[data-view="plugins"].nav-item`):
  - Section: Sidebar
  - Type: nav item
  - Action: Switches the main content to the Plugins list view.
  - Related: Installed plugin table (`#plugin-table`)

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
  - Action: Opens the Add Plugin modal to install a plugin from a URL, native folder picker, or native archive picker.
  - Related: Add Plugin modal (`#add-plugin-modal`), Install from URL (`#install-url-input`)

- **Docs on GitHub** (`#open-docs`):
  - Section: Sidebar
  - Type: button
  - Action: Opens the NusaShell docs directory on GitHub in the system browser.

- **Sidebar display mode** (`#sidebar-mode-toggle`):
  - Section: Sidebar
  - Type: toggle button
  - Action: Switches between icon-only and icon-with-text sidebar modes and remembers the choice on this device.
