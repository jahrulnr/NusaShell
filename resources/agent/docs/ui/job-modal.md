# New Job modal

Form to create a new scheduled job with name, schedule expression, mode (agent prompt or plugin tool call), and optional repeat limit.

**How to open:** Click the + New Job button in the Jobs view.

## Job form

Name, schedule (every 30m / 2h / 1d / 5-field cron / ISO timestamp), mode selector, agent prompt or plugin tool fields, and optional repeat count. Schedule is validated on blur with a live description or error.

- **Name** (`#job-field-name`):
  - Section: Job form
  - Type: text input
  - Action: Job display name.

- **Schedule** (`#job-field-schedule`):
  - Section: Job form
  - Type: text input
  - Action: Schedule expression (every 30m / 2h / 1d / cron / ISO). Validated on blur.

- **Schedule help** (`#job-schedule-help`):
  - Section: Job form
  - Type: status text
  - Action: Live validation result for the schedule expression.

- **Mode** (`#job-field-mode`):
  - Section: Job form
  - Type: select
  - Action: Switches between agent prompt and plugin tool call mode.

- **Prompt** (`#job-field-prompt`):
  - Section: Job form
  - Type: textarea
  - Action: Agent prompt for agent-mode jobs.

- **Plugin ID** (`#job-field-plugin-id`):
  - Section: Job form
  - Type: text input
  - Action: Plugin ID for tool-mode jobs.

- **Tool name** (`#job-field-tool-name`):
  - Section: Job form
  - Type: text input
  - Action: Tool name for tool-mode jobs.

- **Args JSON** (`#job-field-args`):
  - Section: Job form
  - Type: textarea
  - Action: JSON arguments for the tool call.

- **Repeat count** (`#job-field-repeat`):
  - Section: Job form
  - Type: number input
  - Action: How many times to fire (blank = repeat forever).

- **Save** (`#job-modal-save`):
  - Section: Job form
  - Type: button
  - Action: Creates the job via job.add and closes the modal.
