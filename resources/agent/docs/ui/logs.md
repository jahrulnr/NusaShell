# Logs

Live tail of renderer, Electron, IPC, backend, and MCP process log messages.

**How to open:** Click the Logs item in the left sidebar.

## Log filters

Chips filter the tail by source: All, Frontend, Electron, IPC, Backend, and MCP.

- **Log count** (`#log-count`):
  - Section: Log filters
  - Type: status text
  - Action: Shows the number of visible log entries out of the total stored.

- **All logs** (`[data-log-source="all"]`):
  - Section: Log filters
  - Type: chip
  - Action: Shows log entries from all sources.

- **Frontend logs** (`[data-log-source="renderer"]`):
  - Section: Log filters
  - Type: chip
  - Action: Shows only renderer/frontend log entries.

- **Electron logs** (`[data-log-source="main"]`):
  - Section: Log filters
  - Type: chip
  - Action: Shows only Electron main process log entries.

- **IPC logs** (`[data-log-source="ipc"]`):
  - Section: Log filters
  - Type: chip
  - Action: Shows only IPC log entries.

- **Backend logs** (`[data-log-source="backend"]`):
  - Section: Log filters
  - Type: chip
  - Action: Shows only backend log entries.

- **MCP logs** (`[data-log-source="mcp"]`):
  - Section: Log filters
  - Type: chip
  - Action: Shows only MCP process log entries.

## Log tail

Scrollable list of log entries with auto-stick to the bottom. Shows count of visible / total entries.

- **Log tail** (`#log-tail`):
  - Section: Log tail
  - Type: log region
  - Action: Scrollable live tail of log messages. Auto-sticks to the bottom.
