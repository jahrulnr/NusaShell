# Pipelines

Multi-step DAG pipelines that chain multiple agent turns or tool calls with conditional branching and context passing.

**How to open:** Click the Pipelines item in the left sidebar.

## Pipeline list

Lists all pipelines. Each card shows the pipeline name, status, trigger type (event or schedule), step count, and enabled/disabled state. Each card offers Edit, Run now, Enable/Disable, and Delete actions. An inline error panel appears when the list fails to load, and an empty state offers a New pipeline action.

- **New pipeline** (`#pipelines-new-btn`):
  - Section: Pipeline list
  - Type: button
  - Action: Opens the New pipeline modal.

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
