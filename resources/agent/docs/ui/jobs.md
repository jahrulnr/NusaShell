# Jobs

Scheduled automation that fires headless agent turns or plugin tool calls on a once/interval/cron schedule while NusaShell is open.

**How to open:** Click the Jobs item in the left sidebar.

## Job list

Lists all scheduled jobs with their schedule, mode, repeat progress, next run time, and last status. Each row offers Run, Pause/Resume, Output, and Remove actions. A hint reminds the user that jobs run only while the app is open and missed one-shots are marked errored, not silently fired.

- **New Job** (`#jobs-new-btn`):
  - Section: Job list
  - Type: button
  - Action: Opens the New Job modal.

- **Jobs hint** (`#jobs-hint`):
  - Section: Job list
  - Type: status text
  - Action: Explains that jobs run only while the app is open and missed one-shots are marked errored.

- **Jobs list** (`#jobs-list`):
  - Section: Job list
  - Type: list
  - Action: Renders all scheduled jobs as rows.

## Job row actions

Each job row has Run (fire immediately), Pause/Resume (toggle enabled), Output (recent run summaries), and Remove (delete the job).

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
  - Action: Deletes the job after confirmation via job.remove.
