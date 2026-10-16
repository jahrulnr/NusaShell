# Logs

The runtime log tail: a filterable, searchable, followable entry list with a detail reader, bounded to a 2,000-entry ring buffer.

**How to open:** Click the Logs item in the left rail, or open #logs.

## Filters

Search by source or message, filter by level, and follow the live tail; Refresh reloads and Clear wipes the buffer after confirmation.

- **Log search** (`#logs-search`):
  - Type: search
  - Action: Filters entries by source or message.

- **Level filter** (`#logs-level`):
  - Type: select
  - Notes: All, debug, info, warn, error.

- **Entry count** (`#logs-count`):
  - Type: text

- **Visible count** (`#logs-visible-count`):
  - Type: text
  - Action: Entries shown after filters.

- **Follow** (`#logs-follow`):
  - Type: checkbox
  - Notes: Auto-scrolls to the newest entry.

- **Jump to latest** (`#logs-latest`):
  - Type: button
  - Action: Scrolls to the newest entries and resumes follow.

- **Pending entries** (`#logs-pending-count`):
  - Type: text
  - Action: New entries arrived while scrolled up.

- **Refresh logs** (`#logs-refresh`):
  - Type: button

- **Clear logs** (`#logs-clear`):
  - Type: button
  - Action: Clears the on-screen log tail.

- **Logs sync state** (`#logs-sync-state`):
  - Type: status

- **Logs error banner** (`#logs-error`):
  - Type: status

- **Logs error message** (`#logs-error-message`):
  - Type: text

- **Retry loading logs** (`#logs-retry`):
  - Type: button

## List and detail

The list renders entries newest-last with a Latest jump when scrolled up; the detail pane shows source, level, time, id, and the full message. At widths through 720px the page stays viewport-bound and the entry list scrolls inside the bounded workspace card — Follow and Latest target that inner list — while selecting an entry opens one detail page; Back restores the filtered list and preserves follow-tail behavior.

- **Log list and detail workspace** (`#logs-workspace`):
  - Type: panel
  - Notes: Shows the list and entry detail side by side on desktop, and one pane at a time on mobile.

- **Log list** (`#logs-list`):
  - Type: list
  - Notes: Bounded to a 2,000-entry ring buffer.

- **Back to logs** (`#logs-mobile-back`):
  - Type: button
  - Action: Returns to the filtered log list and preserves follow-tail state.

- **Entry source** (`#logs-detail-source`):
  - Type: text

- **Entry level** (`#logs-detail-level`):
  - Type: text

- **Entry time** (`#logs-detail-time`):
  - Type: text

- **Entry ID** (`#logs-detail-id`):
  - Type: text

- **Entry message** (`#logs-detail-message`):
  - Type: text
