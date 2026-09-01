# Automation

Manage durable once/every/when automation workflows, inspect DAG runs, and see waiting or blocked states without occupying a runner.

**How to open:** Click the Automation item in the left sidebar.

## Header stats

Counts of runnable, blocked, and waiting automation workflows so provider outages are visible without opening a run. On small screens the stats wrap below the header copy.

- **`#automation-stat-runnable`** (missing map entry)

- **`#automation-stat-blocked`** (missing map entry)

- **`#automation-stat-waiting`** (missing map entry)

## Toolbar

Tabs switch between Workflows, Runs, Schedules, and Events. New workflow opens a once/every/when/manual wizard. Pipeline files from the data directory appear alongside saved automation workflows in the Workflows tab. On phones the tabs can scroll horizontally without widening the page.

- **`#automation-tabs`** (missing map entry)

- **`#automation-tab-workflows`** (missing map entry)

- **`#automation-tab-runs`** (missing map entry)

- **`#automation-tab-schedules`** (missing map entry)

- **`#automation-tab-events`** (missing map entry)

- **`#automation-new-btn`** (missing map entry)

## Blocked provider

When a saved automation workflow depends on a disabled MCP provider it stays BLOCKED rather than FAILED. Enable provider restores the binding without rewriting the workflow.

- **`#automation-blocked-banner`** (missing map entry)

- **`#automation-blocked-text`** (missing map entry)

- **`#automation-enable-provider-btn`** (missing map entry)

## List and detail

The list shows the active tab. The detail pane renders job needs as a DAG, waiting wake times, and run/enable/disable/cancel actions. Invalid YAML stays listed with an invalid pill; Run and Enable are disabled until the file is fixed.

- **`#automation-list`** (missing map entry)

- **`#automation-list-title`** (missing map entry)

- **`#automation-list-count`** (missing map entry)

- **`#automation-detail`** (missing map entry)

- **`#automation-detail-title`** (missing map entry)

- **`#automation-detail-actions`** (missing map entry)
