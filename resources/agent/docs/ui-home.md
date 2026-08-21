# Home

The launcher view: a grid of installed plugin apps. Each tile opens the plugin UI in a separate browser window. Searchable by name, id, or description and filterable by category.

**How to open:** Open NusaShell, or click the Home item in the left sidebar.

## Filter row

A search input filters the app grid by name, id, or description. Category tabs narrow the grid to a single category. Escape or the clear button resets the search.

- **App search** (`#search-input`):
  - Section: Home
  - Type: search
  - Action: Filters installed apps by name, id, or description. Escape clears the query.
  - Shortcut: Ctrl+K / ⌘K, or / when not typing

- **Clear app search** (`#search-clear`):
  - Section: Home
  - Type: button
  - Action: Clears the app search and returns focus to the search input.

- **App categories** (`#launcher-tabs`):
  - Section: Home
  - Type: tab group
  - Action: Filters the app grid to a category.

## App grid

A responsive grid of plugin tiles. Each tile shows the plugin icon, name, and a runtime status when active. Clicking a tile opens the plugin UI in a new browser window sized from the plugin manifest.

- **App grid** (`#app-grid`):
  - Section: Home
  - Type: grid
  - Action: Click an app tile to open its plugin UI in a separate browser window.
