# Navigation and Shell Chrome

Global chrome surrounding every NusaShell view: title bar, sidebar, connection status, and storage indicator.

**How to open:** Always visible when the NusaShell window is open.

## Title bar

Identifies NusaShell with its brand mark and wordmark, followed by compact backend connection status, a mini-window shortcut, an install shortcut (visible only while the browser offers installation), and a settings shortcut.

- **Connection status text** (`#conn-status`):
  - Section: Title bar
  - Type: text
  - Notes: Reflects backend WebSocket/RPC reachability.

- **Connection orb** (`#conn-fill`):
  - Section: Title bar
  - Type: indicator
  - Notes: Color-coded connection state.

- **Mini window** (`#mini-window-btn`):
  - Section: Navigation
  - Type: button
  - Notes: Opens the picture-in-picture mini window (Document PiP on Chromium, popup fallback elsewhere) showing the live agent thread.

- **Install NusaShell** (`#pwa-install-btn`):
  - Section: Navigation
  - Type: button
  - Notes: Visible only while the browser offers installation (beforeinstallprompt); triggers the native install flow and hides after install/dismissal.

- **Settings shortcut** (`#nav-settings-btn`):
  - Section: Title bar
  - Type: button
  - Action: Opens the Settings view.

## Sidebar

Vertical navigation on the left, ordered Home, Agent, Skills, Learning, Automation, Plugins, Providers, and Logs. It can show icons with labels or icons only, and remembers that choice locally. Below 680px viewport width the sidebar becomes a bounded, vertically scrollable off-canvas drawer: the title-bar hamburger toggles it open, and it closes on backdrop click, Escape, or selecting a nav item. While closed, the drawer is hidden from keyboard and assistive-technology navigation; focus returns to the hamburger after dismissal.

Keyboard: Ctrl/Cmd+K or / focuses the search box on the current view. Escape dismisses dialogs, toasts stay on hover, and Ctrl/Cmd+N starts a new agent conversation when the Agent view is open.

- **Sidebar** (`#sidebar`):
  - Section: Sidebar
  - Type: container
  - Notes: Holds navigation items and storage indicator.

- **Collapse sidebar** (`#sidebar-mode-toggle`):
  - Section: Sidebar
  - Type: button
  - Action: Toggles icon-only sidebar mode (persisted locally).

- **`#mobile-nav-toggle`** (missing map entry)

## Offline screen

Full-window overlay shown whenever the backend is unreachable. It covers every view so no half-broken UI stays interactive: an explicit offline verdict shows it immediately, while closed/error/reconnecting states only cover after a short persistence window so quick reconnects never flicker. Recovery hides it instantly and Try again reloads the shell (service worker serves the cached shell while the server is down).

- **Offline screen** (`#offline-screen`):
  - Section: Navigation
  - Type: status
  - Notes: Full-window overlay covering every view while the backend is unreachable; hidden again the moment the connection reopens.

- **Try again** (`#offline-retry-btn`):
  - Section: Navigation
  - Type: button
  - Notes: Reloads the shell; the service worker serves the cached app while the server is down.
