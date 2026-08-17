# Navigation and Shell Chrome

Global chrome surrounding every NusaShell view: title bar, sidebar, connection status, and storage indicator.

**How to open:** Always visible when the NusaShell window is open.

## Title bar

Identifies NusaShell with its brand mark and wordmark, followed by compact backend connection status and a settings shortcut.

- **Connection status text** (`#conn-status`):
  - Section: Title bar
  - Type: text
  - Notes: Reflects backend WebSocket/RPC reachability.

- **Connection orb** (`#conn-fill`):
  - Section: Title bar
  - Type: indicator
  - Notes: Color-coded connection state.

- **Settings shortcut** (`#nav-settings-btn`):
  - Section: Title bar
  - Type: button
  - Action: Opens the Settings view.

## Sidebar

Vertical navigation on the left, ordered Home, Agent, Skills, Learning, Automation, Plugins, Providers, and Logs. It can show icons with labels or icons only, and remembers that choice locally.

- **Sidebar** (`#sidebar`):
  - Section: Sidebar
  - Type: container
  - Notes: Holds navigation items and storage indicator.

- **Collapse sidebar** (`#sidebar-mode-toggle`):
  - Section: Sidebar
  - Type: button
  - Action: Toggles icon-only sidebar mode (persisted locally).

## Storage

Shows the absolute data directory path and a usage bar so the user can see where conversations, providers, memory, and credentials live.

- **Data directory path** (`#storage-path`):
  - Section: Storage
  - Type: text
  - Notes: Absolute path to the data directory.
