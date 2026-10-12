# Logs

Runtime events streamed live from the shell. The log file is a bounded ring (2000 entries).

**How to open:** Click the Logs item in the left sidebar.

## Filters

Level filter (All/debug/info/warn/error), entry count, Follow toggle, and Clear button.

- **Level filter** (`#log-level-filter`):
  - Type: select
  - Notes: All, debug, info, warn, error.

- **Entry count** (`#log-count`):
  - Type: text

- **Follow** (`#log-follow`):
  - Type: checkbox
  - Notes: Auto-scrolls to the newest entry.

- **Clear** (`#logs-clear-btn`):
  - Type: button
  - Action: Clears the persisted runtime log and the view.

## Tail

Scrolling list of log entries. When Follow is on, the tail auto-scrolls to the newest entry.

- **Log tail** (`#log-tail`):
  - Type: list
