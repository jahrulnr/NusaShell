# Jobs

Scheduled automation that fires headless agent turns or plugin tool calls on a once/interval/cron schedule while NusaShell is open.

**How to open:** Click the Jobs item in the left sidebar.

## Job list

Lists all scheduled jobs. Each row leads with a status dot and shows the job name, schedule, mode, and repeat progress, plus a schedule strip with a humanized next run time and a textual last status (Never / OK / Error). Each row offers Run, Pause/Resume, Output, and Remove actions. A hint reminds the user that jobs run only while the app is open and missed one-shots are marked errored, not silently fired. An inline error panel appears when the list fails to load, and an empty state offers a New job action.

- **New job** (`#jobs-new-btn`):
  - Section: Job list
  - Type: button
  - Action: Opens the New job modal.

- **Jobs hint** (`#jobs-hint`):
  - Section: Job list
  - Type: status text
  - Action: Explains that jobs run only while the app is open and missed one-shots are marked errored.

- **Jobs list** (`#jobs-list`):
  - Section: Job list
  - Type: list
  - Action: Renders all scheduled jobs as rows.

- **Jobs empty state** (`#jobs-empty`):
  - Section: Job list
  - Type: status text
  - Action: Shown when no jobs are scheduled, with a direction and a New job action.

- **New job (empty state)** (`#jobs-empty-new-btn`):
  - Section: Job list
  - Type: button
  - Action: Opens the New job modal from the empty state.

- **Jobs load error** (`#jobs-error`):
  - Section: Job list
  - Type: status text
  - Action: Inline error panel shown when the job list fails to load.

## Job row actions

Each job row has Run (fire immediately), Pause/Resume (toggle enabled), Output (recent run summaries), and Remove (opens a delete confirmation dialog).

- **Run** (`[data-control="job-run-btn"]`):
  - Section: Job row actions
  - Type: button
  - Action: Fires the job immediately via job.run.

- **Pause/Resume** (`[data-control="job-toggle-btn"]`):
  - Section: Job row actions
  - Type: button
  - Action: Toggles job enabled state via job.set-enabled.

- **Output** (`[data-control="job-output-btn"]`):
  - Section: Job row actions
  - Type: button
  - Action: Opens the job output modal showing recent run summaries.

- **Remove** (`[data-control="job-remove-btn"]`):
  - Section: Job row actions
  - Type: button
  - Action: Opens the job delete confirmation dialog.

## Job delete confirmation

An in-app dialog confirms job removal before deleting it via job.remove. It opens from a row's Remove action and closes on Cancel, overlay click, or Escape.

- **Delete dialog** (`#job-delete-dialog`):
  - Section: Job delete confirmation
  - Type: dialog
  - Action: Confirms job removal before calling job.remove. Closes on Cancel, overlay click, or Escape.

- **Delete copy** (`#job-delete-copy`):
  - Section: Job delete confirmation
  - Type: status text
  - Action: States which job will be permanently removed from this device.

- **Cancel** (`#job-delete-cancel`):
  - Section: Job delete confirmation
  - Type: button
  - Action: Closes the delete dialog without removing the job.

- **Remove** (`#job-delete-confirm`):
  - Section: Job delete confirmation
  - Type: button
  - Action: Removes the job via job.remove and closes the dialog.
