# Pipelines

Beta multi-step DAG pipelines (manual Run now and event triggers while the app is open). Schedule triggers are not available; use Jobs for time-based work.

**How to open:** Click the Pipelines item in the left sidebar.

## Pipeline list

Lists all pipelines as compact execution rails. Each card shows the pipeline identity, status signal, trigger, step flow, enabled state, and primary Inspect/Run actions; edit, pause, and delete live behind the overflow menu. An inline error panel appears when the list fails to load, and an empty state offers a Create first pipeline action.

- **New pipeline** (`#pipelines-new-btn`):
  - Section: Pipeline list
  - Type: button
  - Action: Opens the New pipeline modal.

- **Create first pipeline** (`[data-control="pipelines-empty-new"]`):
  - Section: Pipeline list
  - Type: button
  - Action: Opens the New pipeline modal from the empty state.

- **Pipeline list** (`#pipelines-list`):
  - Section: Pipeline list
  - Type: list
  - Action: Renders pipeline cards with status, trigger, step count, and actions.

- **Pipelines empty state** (`#pipelines-empty`):
  - Section: Pipeline list
  - Type: status text
  - Action: Shown when no pipelines exist.

- **Pipelines error** (`#pipelines-error`):
  - Section: Pipeline list
  - Type: error panel
  - Action: Shown when the pipeline list fails to load.

## Pipeline details

Inspect opens a stable DAG view that keeps every configured step visible before, during, and after a run. Click any step node to inspect its action, dependencies, output key, timestamps, status, definition, and rendered Markdown output. Run output remains below the DAG and is grouped by step.

- **Cancel run** (`#pipeline-details-cancel`):
  - Section: Pipeline details
  - Type: button
  - Action: Requests cancellation of the in-flight pipeline run via pipeline.cancel.
