# Home (Launcher)

The default view and app launcher. Shows windowed (UI) plugins as a searchable grid of app tiles. Headless MCP-only plugins do not appear here — manage them from the Plugins view.

**How to open:** Open NusaShell, or click the Home item in the left sidebar.

## Greeting

Displays a compact local-workbench kicker, the product name, and the tagline 'Your AI tool shell — plugins with real UIs.'

## Search and filters

The Home search filters installed plugin cards by name, id, or description in real time. It spans the launcher above a compact, horizontally scrollable category rail so both filters read as one deliberate sequence.

- **Installed app search** (`#search-input`):
  - Section: Search and filters
  - Type: search input
  - Action: Filters only the Home app grid by plugin name, plugin id, or description as the user types.
  - Shortcut: Escape clears the current query when the input is focused.
  - Related: Clear search (`#search-clear`), Installed plugin grid (`#app-grid`)

- **Clear search** (`#search-clear`):
  - Section: Search and filters
  - Type: button
  - Action: Clears the global app search field and refocuses it.
  - Related: Installed app search (`#search-input`)

- **Filter tabs** (`.tabs .tab`):
  - Section: Search and filters
  - Type: tab group
  - Action: Tabs above the app grid that scope the list of plugins. Defaults to All.

## App grid

A grid of installed UI plugins (plugins whose manifest declares a `ui.entry`). Each tile uses an instrument-style launch plate and shows the plugin icon, name, and a textual state badge (idle, starting, running, stopping, crashed). Headless MCP-only plugins are filtered out of Home and managed from the Plugins view. Emoji and image artwork share one normalized plate; transparent margins in local PNG artwork are normalized at render time so mixed icon sources keep comparable visual weight.

- **Installed plugin grid** (`#app-grid`):
  - Section: App grid
  - Type: grid
  - Action: Container for plugin tiles. Renders an empty message when no plugins match or are installed.
  - Related: Plugin tile (`.app-cell`)

- **Plugin tile** (`.app-cell`):
  - Section: App grid
  - Type: button
  - Action: Click to open the plugin window. Right-click to open the plugin context menu. Shows icon, name, and state badge.
  - Related: Context menu (`#context-menu`)

## Empty state

When no plugins are installed, the grid shows a message pointing to the example plugin folder.
