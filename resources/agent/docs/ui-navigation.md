# Navigation and Shell Chrome

Global chrome surrounding every NusaShell view: the vertical sidebar owns identity, connection status, utilities, navigation, and storage; a small mobile bar only exposes drawer navigation.

**How to open:** Always visible when the NusaShell window is open.

## Sidebar identity and utilities

The vertical sidebar identifies NusaShell with its brand mark and wordmark, shows compact backend connection status, and keeps settings plus the install shortcut (visible only while the browser offers installation) with the navigation. The desktop pet launcher sits above the Settings icon: it surfaces install/launch status via a coloured dot (green when installed, pulsing amber while an install is active, neutral on macOS/Windows where the pet is not supported) and opens a one-click install dialog when the binary is missing.

- **Connection status text** (`#conn-status`):
  - Section: Sidebar
  - Type: text
  - Notes: Reflects backend WebSocket/RPC reachability.

- **Connection orb** (`#conn-fill`):
  - Section: Sidebar
  - Type: indicator
  - Notes: Color-coded connection state.

- **Install NusaShell** (`#pwa-install-btn`):
  - Section: Sidebar
  - Type: button
  - Notes: Visible only while an eligible browser offers installation (beforeinstallprompt); hidden in standalone/PWA and Electron runtimes; triggers the native install flow and hides after install/dismissal.

- **Desktop pet** (`#pet-btn`):
  - Section: Sidebar
  - Type: button
  - Action: Single click when installed toggles the desktop pet overlay (settings.pets_launch spawns if idle, stops if running). When not installed, opens a one-click install dialog backed by settings.pets_install_start.
  - Notes: Hidden on macOS/Windows (pet is Linux-only). Sits above the Settings icon in .sidebar-actions.

- **Settings shortcut** (`#nav-settings-btn`):
  - Section: Sidebar
  - Type: button
  - Action: Opens the Settings view.

## Mobile navigation bar

Desktop has no separate titlebar. Below 680px, a compact bar remains only as the reliable home for the drawer toggle while the sidebar itself is off-canvas.

- **Open navigation menu** (`#mobile-nav-toggle`):
  - Section: Mobile navigation bar
  - Type: button
  - Action: Opens or closes the off-canvas navigation drawer below 680px.

## Sidebar

Vertical navigation on the left, ordered Home, Agent, Skills, Learning, Automation, Plugins, Providers, Logs, and Telemetry. It can show icons with labels or icons only, and remembers that choice locally. Below 680px viewport width the sidebar becomes a bounded, vertically scrollable off-canvas drawer: the mobile navigation bar hamburger toggles it open, and it closes on backdrop click, Escape, or selecting a nav item. While closed, the drawer is hidden from keyboard and assistive-technology navigation; focus returns to the hamburger after dismissal.

Keyboard: Ctrl/Cmd+K or / focuses the search box on the current view. Escape dismisses dialogs, toasts stay on hover, and Ctrl/Cmd+N starts a new agent conversation when the Agent view is open.

- **Sidebar** (`#sidebar`):
  - Section: Sidebar
  - Type: container
  - Notes: Holds navigation items and storage indicator.

- **Collapse sidebar** (`#sidebar-mode-toggle`):
  - Section: Sidebar
  - Type: button
  - Action: Toggles icon-only sidebar mode (persisted locally).

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

## Device pairing gate

Full-window dialog shown when the backend requires device pairing (an unpaired non-loopback client). While the gate owns the screen, the offline overlay and view error toasts are suppressed so the pairing state is the single explanation. It renders pending/waiting, success, and terminal link states such as expired, rejected, already used, or invalid without offering a retry that cannot make the same link valid; transient backend/network failures can still be retried.

- **Device pairing gate** (`#pairing-gate`):
  - Section: Navigation — Overlays
  - Type: dialog
  - Notes: Full-window dialog shown when the backend requires device pairing; owns the screen while PAIRING_REQUIRED.

- **Pairing gate title** (`#pairing-gate-title`):
  - Section: Navigation — Overlays
  - Type: heading
  - Notes: Receives focus when the gate opens.

- **Pairing gate explanation** (`#pairing-gate-intro`):
  - Section: Navigation — Overlays
  - Type: text
  - Notes: Explains either the pairing requirement or that remote access is disabled on the host.

- **Pairing pending** (`#pairing-pending`):
  - Section: Navigation — Overlays
  - Type: status
  - Notes: Waiting-for-host-approval state.

- **Pairing status text** (`#pairing-status`):
  - Section: Navigation — Overlays
  - Type: status

- **Pairing success** (`#pairing-success`):
  - Section: Navigation — Overlays
  - Type: status

- **Pairing error** (`#pairing-error`):
  - Section: Navigation — Overlays
  - Type: alert

- **Pairing error message** (`#pairing-error-message`):
  - Section: Navigation — Overlays
  - Type: text

- **Retry pairing** (`#pairing-retry-btn`):
  - Section: Navigation — Overlays
  - Type: button
  - Action: Retries the pairing status/exchange flow.

- **Pairing help** (`#pairing-help`):
  - Section: Navigation — Overlays
  - Type: panel
  - Notes: Explains how to open the host QR and pair.

- **Pairing help toggle** (`#pairing-help-btn`):
  - Section: Navigation — Overlays
  - Type: button
  - Action: Shows or hides the pairing help panel.

- **Close pairing help** (`#pairing-help-close`):
  - Section: Navigation — Overlays
  - Type: button

- **Challenge countdown** (`#pairing-countdown`):
  - Section: Navigation — Overlays
  - Type: status
  - Notes: Remaining challenge lifetime.
