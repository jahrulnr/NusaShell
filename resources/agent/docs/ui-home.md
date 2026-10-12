# Home

The launcher view: a grid of installed plugin apps. Each tile opens (or focuses, when it is already open) the plugin UI in a separate browser window. Searchable by name, id, or description and filterable by category.

**How to open:** Open NusaShell, or click the Home item in the left sidebar.

## Filter row

A search input filters the app grid by name, id, or description. Category tabs narrow the grid to a single category. Escape or the clear button resets the search.

- **App search** (`#search-input`):
  - Type: search
  - Action: Filters installed apps by name, id, or description. Escape clears the query.
  - Shortcut: Ctrl+K / ⌘K, or / when not typing

- **Clear app search** (`#search-clear`):
  - Type: button
  - Action: Clears the app search and returns focus to the search input.

- **App categories** (`#launcher-tabs`):
  - Type: tab group
  - Action: Filters the app grid to a category.

## App grid

A responsive grid of plugin tiles. Each tile shows the plugin icon (including local manifest icons resolved for the browser), name, and a runtime status when active. Artwork is contained inside the instrument-style icon plate even when a manifest image has an unusual aspect ratio. Clicking a tile opens (or focuses, when already open) the plugin UI in a separate browser window sized from the plugin manifest. If the plugin list fails to load, an error banner offers a Retry button that refetches it.

- **App grid** (`#app-grid`):
  - Type: grid
  - Action: Click an app tile to open its plugin UI in a separate browser window; clicking again focuses the already-open window rather than spawning a duplicate.

- **Plugin load error** (`#plugin-load-error`):
  - Type: status
  - Notes: Alert banner shown above the grid when the plugin list fails to load; includes the failure message and a Retry button.

- **Retry plugin load** (`#plugin-load-retry`):
  - Type: button
  - Action: Refetches the plugin list after a load failure.
