# Logs

Runtime events streamed live from the shell via WebSocket. The log file is a bounded ring (2000 entries) persisted as JSONL.

**How to open:** Click the Logs item in the left sidebar.

## Filters

Level filter (All/debug/info/warn/error), entry count, Follow toggle, and Clear button.

- **Level filter** (`#log-level-filter`):
  - Section: Logs
  - Type: select
  - Notes: All, debug, info, warn, error.

- **Entry count** (`#log-count`):
  - Section: Logs
  - Type: text

- **Follow** (`#log-follow`):
  - Section: Logs
  - Type: checkbox
  - Notes: Auto-scrolls to the newest entry.

- **Clear** (`#logs-clear-btn`):
  - Section: Logs
  - Type: button
  - Action: Clears the on-screen log tail.

## Tail

Scrolling list of log entries. When Follow is on, the tail auto-scrolls to the newest entry.

- **Log tail** (`#log-tail`):
  - Section: Logs
  - Type: list
