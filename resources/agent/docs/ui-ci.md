# CI

Manage durable once/every/when CI workflows, inspect DAG runs, and see waiting or blocked states without occupying a runner.

**How to open:** Click the CI item in the left sidebar.

## Header stats

Counts of runnable, blocked, and waiting CI workflows so provider outages are visible without opening a run. On small screens the stats wrap below the header copy.

- **`#ci-stat-runnable`** (missing map entry)

- **`#ci-stat-blocked`** (missing map entry)

- **`#ci-stat-waiting`** (missing map entry)

## Toolbar

Tabs switch between Workflows, Runs, Schedules, and Events. New workflow opens a once/every/when/manual wizard. Pipeline files from the data directory appear alongside saved CI workflows in the Workflows tab. On phones the tabs can scroll horizontally without widening the page.

- **`#ci-tabs`** (missing map entry)

- **`#ci-tab-workflows`** (missing map entry)

- **`#ci-tab-runs`** (missing map entry)

- **`#ci-tab-schedules`** (missing map entry)

- **`#ci-tab-events`** (missing map entry)

- **`#ci-new-btn`** (missing map entry)

## Blocked provider

When a saved CI workflow depends on a disabled MCP provider it stays BLOCKED rather than FAILED. Enable provider restores the binding without rewriting the workflow.

- **`#ci-blocked-banner`** (missing map entry)

- **`#ci-blocked-text`** (missing map entry)

- **`#ci-enable-provider-btn`** (missing map entry)

## List and detail

The list shows the active tab. The detail pane renders job needs as a DAG, waiting wake times, and run/enable/disable/cancel actions. Invalid YAML stays listed with an invalid pill; Run and Enable are disabled until the file is fixed.

- **`#ci-list`** (missing map entry)

- **`#ci-list-title`** (missing map entry)

- **`#ci-list-count`** (missing map entry)

- **`#ci-detail`** (missing map entry)

- **`#ci-detail-title`** (missing map entry)

- **`#ci-detail-actions`** (missing map entry)
