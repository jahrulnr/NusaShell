# Automation

The automation control room: workflows, runs, schedules, and event records in one browse column with stat cards, a mode switch, search, and an inspector with detail, steer/delete actions, and the YAML workflow editor.

**How to open:** Click the Automation item in the left rail, or open #automation.

## Header

Refresh, sync state, and the New workflow action; errors collapse into a retry banner.

- **Automation sync state** (`#automation-sync-state`):

- **Refresh automation** (`#automation-refresh`):

- **New workflow** (`#automation-create`):

- **Automation error banner** (`#automation-error`):

- **Error message** (`#automation-error-message`):

- **Retry loading** (`#automation-retry`):

## Stats

Counts of runnable, needs-attention, in-progress, and waiting items across workflows and runs.

- **Runnable count** (`#automation-stat-runnable`):

- **Needs attention count** (`#automation-stat-blocked`):

- **In progress count** (`#automation-stat-active`):

- **Waiting count** (`#automation-stat-waiting`):

## Browse

The mode switch filters workflows, runs, and event records; search narrows the list by name or state; the schedule rail lists upcoming runs. Blocked workflows show a callout with the reason and an enable-provider action when a capability is disabled.

On mobile through 720px, Runs and Events use a full-width feed with sticky tabs and search. Opening a record shows its detail separately; Back returns to the same filter, selection and reading position. Workflow overview and desktop split panes remain available.

- **Mode switch** (`[data-automation-mode]`):
  - Type: button group
  - Action: Filters the browse list between workflows, runs, and events.

- **Automation search** (`#automation-search`):

- **Automation list** (`#automation-list`):

- **List count** (`#automation-list-count`):

- **Next scheduled** (`#automation-schedule-title`):

- **Scheduled runs** (`#automation-schedule-list`):

- **Back to history** (`#automation-mobile-back`):
  - Type: button
  - Action: Returns from a run or event detail to the mobile history feed, retaining the filter, selection and scroll position.
  - Notes: Shown only for mobile Runs and Events details through 720px.

## Inspector and editor

The inspector renders the selected workflow (capabilities, enable/run/delete/steer actions), run (per-step progress and output, cancel/steer), or the YAML editor with validation.

- **Automation inspector** (`#automation-inspector`):

- **Inspector empty state** (`#automation-inspector-empty`):

- **Create a workflow** (`#automation-empty-create`):

- **Workflow detail** (`#automation-workflow-detail`):

- **Run detail** (`#automation-run-detail`):

- **Workflow editor** (`#automation-editor`):

- **Editor title** (`#automation-editor-title`):

- **Close editor** (`#automation-editor-close`):

- **Editor form** (`#automation-editor-form`):

- **Workflow name** (`#automation-editor-name`):

- **Workflow YAML** (`#automation-editor-yaml`):

- **Validation output** (`#automation-validation`):

- **Cancel editor** (`#automation-editor-cancel`):

- **Check YAML** (`#automation-editor-check`):

- **Create workflow** (`#automation-editor-save`):
