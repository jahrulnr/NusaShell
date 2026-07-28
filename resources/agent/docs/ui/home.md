# Home (Launcher)

The default view and app launcher. Shows installed plugins as a searchable grid of app tiles.

**How to open:** Open NusaShell, or click the Home item in the left sidebar.

## Greeting

Displays the product name, greeting, and the tagline 'Your AI tool shell — plugins with real UIs.'

## Search and filters

The title bar search filters the grid in real time. Filter tabs below the greeting let you scope the list.

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

- **Filter tabs** (`.tabs .tab`):
  - Section: Search and filters
  - Type: tab group
  - Action: Tabs above the app grid that scope the list of plugins. Defaults to All.

## App grid

A grid of installed plugins. Each tile shows the plugin icon, name, and a state badge (idle, starting, running, stopping, crashed).

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
