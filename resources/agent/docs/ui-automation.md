# Automation

Manage durable once/every/when automation workflows, inspect DAG runs, and see waiting or blocked states without occupying a runner.

**How to open:** Click the Automation item in the left sidebar.

## Header stats

Counts of runnable, blocked, and waiting automation workflows so provider outages are visible without opening a run. On small screens the stats wrap below the header copy.

- **Runnable count** (`#automation-stat-runnable`):
  - Type: text
  - Notes: Header stat: workflows whose availability is runnable.

- **Blocked count** (`#automation-stat-blocked`):
  - Type: text
  - Notes: Header stat: workflows blocked because a required capability provider is disabled.

- **Waiting count** (`#automation-stat-waiting`):
  - Type: text
  - Notes: Header stat: runs waiting for a wake time or event without occupying a runner.

## Toolbar

Tabs switch between Workflows, Runs, Schedules, and Events in the same compact segmented control used by Learning. New workflow opens a once/every/when/manual wizard. Pipeline files from the data directory appear alongside saved automation workflows in the Workflows tab. On phones the tabs can scroll horizontally without widening the page.

- **Automation tabs** (`#automation-tabs`):
  - Type: tablist
  - Notes: Compact segmented control switching the workspace between Workflows, Runs, Schedules, and Events.

- **Workflows tab** (`#automation-tab-workflows`):
  - Type: tab
  - Notes: Saved automation workflows plus pipeline files discovered in the data directory.

- **Runs tab** (`#automation-tab-runs`):
  - Type: tab
  - Notes: Automation run history with status pills and wake times.

- **Schedules tab** (`#automation-tab-schedules`):
  - Type: tab
  - Notes: Trigger schedules with kind, status, and next fire time.

- **Events tab** (`#automation-tab-events`):
  - Type: tab
  - Notes: Recorded automation events.

- **New workflow** (`#automation-new-btn`):
  - Type: button
  - Action: Opens the new-automation wizard dialog: name, trigger family (Once/Every/When/Manual), a time-or-event field, and the Jobs YAML; validates then saves enabled.

## Blocked provider

When a saved automation workflow depends on a disabled MCP provider it stays BLOCKED rather than FAILED. Enable provider restores the binding without rewriting the workflow.

- **Blocked provider banner** (`#automation-blocked-banner`):
  - Type: banner
  - Notes: Shown while at least one workflow is BLOCKED by a disabled capability provider; hidden otherwise.

- **Blocked reason** (`#automation-blocked-text`):
  - Type: text
  - Notes: The first blocked workflow's blocked reason, or a generated '<name> is blocked.' message.

- **Enable provider** (`#automation-enable-provider-btn`):
  - Type: button
  - Action: Re-enables the disabled capability provider so blocked workflows become runnable again.

## List and detail

The list shows the active tab. The detail pane renders job needs as a DAG, waiting wake times, and run/enable/disable/cancel actions. Invalid YAML stays listed with an invalid pill; Run and Enable are disabled until the file is fixed.

- **Automation list** (`#automation-list`):
  - Type: list
  - Action: Lists rows for the active tab (title, status pill, meta); clicking a row opens its detail.
  - Notes: Invalid YAML files stay listed with an 'invalid' pill.

- **List pane title** (`#automation-list-title`):
  - Type: text
  - Notes: Mirrors the active tab name (Workflows / Runs / Schedules / Events).

- **List row count** (`#automation-list-count`):
  - Type: text

- **Automation tab panel** (`#automation-workspace`):
  - Type: tabpanel
  - Notes: Shared list and detail panel; its accessible label follows the active Workflows, Runs, Schedules, or Events tab.

- **Automation detail pane** (`#automation-detail`):
  - Type: container
  - Notes: Renders the selection: a workflow DAG with job needs and steps, run job status with step output, waiting wake times, the blocked reason, or raw JSON for events.

- **Detail title** (`#automation-detail-title`):
  - Type: text

- **Detail actions** (`#automation-detail-actions`):
  - Type: container
  - Notes: Per-selection buttons: workflows expose Run, Enable/Disable, and Delete (Run is disabled for invalid files; Enable is also disabled while an invalid file is already disabled); running, queued, or waiting runs expose Cancel plus Steer while an agent step is in-flight; failed runs expose Retry.
