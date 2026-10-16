# Home

The launcher view: a grid of installed plugin apps. Each tile opens the plugin UI in a separate browser window. Searchable by name, id, or description and filterable by category.

**How to open:** Open NusaShell, or click the Home item in the left rail.

## Filter row

A search input filters the app grid by name, id, or description. Category tabs narrow the grid to a single category (All plus every category present in the installed set, with an Uncategorized bucket when needed). Escape resets the search.

- **App search** (`#home-search`):
  - Type: search
  - Action: Filters installed apps by name, id, or description. Escape clears the query.
  - Shortcut: Ctrl+K / ⌘K, or / when not typing

- **App categories** (`#home-category-tabs`):
  - Type: tab group
  - Action: Filters the app grid to a single category; All resets. Hidden until categories load.

## App grid

A responsive grid of plugin tiles. Each tile shows the plugin icon (including local manifest icons resolved for the browser), name, and a runtime status when active. Clicking a tile opens the plugin UI in a new browser window sized from the plugin manifest. While the backend requires pairing, the grid stays quiet so the pairing gate dominates; the Retry affordance reloads the list. At widths through 720px the same launcher opens a normal browser tab instead of a floating window.

- **App grid** (`#home-app-grid`):
  - Type: grid
  - Action: Click an app tile to open its plugin UI through the shared launcher: a separate floating window on desktop, a normal browser tab at widths through 720px.

- **App count** (`#home-app-count`):
  - Type: text
  - Notes: Visible vs installed count while a filter is active.

- **Launcher status** (`#home-app-message`):
  - Type: status
  - Notes: Loading, empty, error, and pairing-quiet states for the launcher.

- **Retry loading apps** (`#home-app-retry`):
  - Type: button
  - Action: Reloads the app grid after a failed fetch.
