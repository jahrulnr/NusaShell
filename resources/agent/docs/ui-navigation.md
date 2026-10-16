# Navigation and Shell Chrome

Global chrome surrounding every NusaShell view: a top bar owns the drawer toggles, brand, room heading, context meter, and dock toggle; the left rail holds navigation and utilities.

**How to open:** Always visible when the NusaShell window is open.

## Top bar

The top bar anchors the shell: hamburger and room-drawer toggles on the left, the NusaShell mark and room heading in the center, and the context meter plus conversation-dock toggle on the right. The context meter is visible only on Agent, where it opens the Context inspector; its ring fill tracks context usage while a data-connection state reflects WebSocket reachability. The room-drawer, context-meter, and dock toggles stay in the DOM on every route but are visible only while the Agent view is active (`.shell` carries `agent-active` only there); the narrow-width overlay backdrop is likewise scoped to the Agent surface so it can never cover a panel-less page. The room heading shows the active conversation title only while the Agent route owns the surface; every other route paints its own page label and asynchronous conversation updates cannot overwrite it.

Primary view titles are shown once in the global titlebar; duplicate in-page titles remain accessible to assistive technology without consuming visible space. Body-level toasts use the shell palette, a dismiss action, and a bounded mobile width.

- **Navigation toggle** (`#global-nav-toggle`):
  - Type: button
  - Action: Toggles the global navigation rail open or closed.

- **Conversations drawer toggle** (`#room-drawer-toggle`):
  - Type: button
  - Action: Toggles the room drawer (conversation list) open or closed.
  - Related: Room drawer (`#room-drawer`), New chat (`#new-chat`)
  - Notes: Agent-only control: always present in the top-bar DOM but hidden on every non-Agent route (`.shell:not(.agent-active)` display:nones it); leaving Agent also closes any open overlay panel so the shared backdrop cannot strand over a panel-less page.

- **Context meter** (`#context-meter`):
  - Type: button
  - Action: Opens the context dock Context panel; ring fill tracks the active room context usage.
  - Notes: Agent-only control: it is hidden on non-Agent routes because only Agent owns the Context inspector. Doubles as the backend connection indicator: data-connection mirrors WebSocket reachability and its title reads Backend connected / Backend <state>. Context thresholds: data-context=high at >=80%, critical at >=95% of the active model context window.

- **Dock toggle** (`#context-dock-toggle`):
  - Type: button
  - Action: Toggles the conversation context dock open or closed.
  - Related: Context dock (`#context-dock`)
  - Notes: Agent-only control: always present in the top-bar DOM but hidden on every non-Agent route (`.shell:not(.agent-active)` display:nones it); leaving Agent also closes any open overlay panel so the shared backdrop cannot strand over a panel-less page.

## Global navigation rail

The left rail orders Home, Agent, Skills, Learning, Automation, Plugins, Providers, Logs, Telemetry, and Settings as icon links. It can show icons with labels or icons only and remembers that choice locally. The install shortcut appears only while the browser offers installation; the desktop pet entry sits just above Settings (hidden at the <=720px overlay breakpoint): it surfaces install/launch status and opens the install dialog when the binary is missing.

Both Install and Pet drive hidden production-ID proxies (pwa-install-btn, pet-btn) so the shared pwa.js and pet-launcher.js modules work unchanged; the visible rail items are global-nav-install and global-nav-pet.

Below the nav breakpoints the rail becomes an off-canvas drawer: the top-bar nav toggle opens it and moves focus to the active rail item. It closes on backdrop click, Escape, selecting a nav item, or opening the room drawer/context dock — at the <=720px overlay breakpoint the rail and both conversation panels are mutually exclusive, so only one fixed overlay stays open at a time and no scrim can strand over a live panel — and focus returns to the toggle. Keyboard: Ctrl/Cmd+K or / focuses the search box on the current view, Escape dismisses dialogs, Ctrl/Cmd+N starts a new agent conversation when the Agent view is open.

Mobile Skills, Providers, Plugins, Logs, Learning, and Automation reopen at their index or primary tab when entered from navigation. In-page Back restores reading position; unsaved editor content is retained.

- **Global navigation rail** (`#global-nav`):
  - Section: Navigation rail
  - Type: container
  - Notes: Left icon rail holding the 11 route items plus Install, Pet, Settings, and the compact-mode toggle.

- **Navigation items** (`[data-global-route]`):
  - Section: Navigation rail
  - Type: link list
  - Action: Each item routes the shell to its view via hash links (#home, #agent, #skills, #learning, #automation, #plugins, #providers, #logs, #telemetry, #settings).
  - Notes: Anchors with data-global-route carrying the hash route; the active one carries is-active and aria-current=page.

- **Expand sidebar** (`#global-nav-expand`):
  - Section: Navigation rail
  - Type: button
  - Action: Toggles the rail between icon-only and icon+label modes; the choice persists in localStorage (nusashell.sidebarMode).
  - Notes: At <=720px the rail is an always-labelled overlay drawer, so the same footer slot becomes a Close sidebar action that dismisses the drawer instead of toggling a mode it cannot show.

- **Install app** (`#global-nav-install`):
  - Section: Navigation rail
  - Type: button
  - Action: Shows only while the browser offers PWA installation; installs NusaShell as an app.
  - Notes: Drives the hidden pwa-install-btn proxy so js/pwa.js keeps working.

- **Desktop pet** (`#global-nav-pet`):
  - Section: Navigation rail
  - Type: button
  - Action: Opens the desktop pet launcher: routes to Settings pet card and dispatches the pet launcher event.
  - Notes: Drives the hidden pet-btn proxy via nusashell:open-pet-launcher so js/pet-launcher.js keeps working; a status dot on the icon reflects install state. Hidden at widths through 720px — the pet launcher is a desktop-only affordance.

- **Install app proxy** (`#pwa-install-btn`):
  - Section: Navigation rail
  - Type: button
  - Action: Hidden production-ID proxy bound by js/pwa.js initInstallButton; global-nav-install is the visible affordance.
  - Notes: Kept inside a hidden wrapper so module state flips never render a duplicate control.

- **Desktop pet proxy** (`#pet-btn`):
  - Section: Navigation rail
  - Type: button
  - Action: Hidden production-ID proxy bound by js/pet-launcher.js (install dialog, launch toggle).
  - Notes: Kept inside a hidden wrapper; global-nav-pet is the visible affordance.

- **Navigation backdrop** (`#mobile-nav-backdrop`):
  - Section: Navigation rail
  - Type: overlay
  - Action: Click to close the open navigation drawer on small screens.

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

## App loading layer

Full-window boot overlay shown while the shell loads its first data; it hides once the app is interactive.

- **Loading layer** (`#page-loading`):
  - Section: Boot
  - Type: overlay
  - Notes: Full-window boot overlay removed once the shell is interactive.
