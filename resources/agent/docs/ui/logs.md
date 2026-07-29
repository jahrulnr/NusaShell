# Logs

Live tail of renderer, Electron, IPC, backend, and MCP process log messages, with retained counts per producer.

**How to open:** Click the Logs item in the left sidebar.

## Log filters

Chips filter the tail by source and show how many retained entries each producer has. Electron receives main-process console output, IPC receives window and tool-call activity, and Backend receives server logger output; an empty source explains what action produces entries.

- **Log count** (`#log-count`):
  - Section: Log filters
  - Type: status text
  - Action: Shows the total retained entries against the 1,000-entry limit. Each source chip shows its own retained count.

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

The log card fills the remaining shell viewport and scrolls internally, so tall windows do not leave a detached empty area below it. The tail auto-sticks to the bottom and shows retained counts.

- **Log tail** (`#log-tail`):
  - Section: Log tail
  - Type: log region
  - Action: Scrollable live tail of log messages. Auto-sticks to the bottom.
