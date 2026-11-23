# Schedule an automation

An automation starts a prompt on a schedule and records its run in a conversation. Use one for repeatable checks, summaries, or reminders that should run while NusaShell is available.

## Create a schedule

1. Open **Settings → Automations**.
2. Choose **New automation** and give it a name.
3. Write the prompt that should run.
4. Choose an agent, or a provider and model.
5. Choose a one-time run or enter a five-field cron schedule, and select its timezone.
6. Choose whether each run starts a new conversation or reuses a dedicated one. You can also select a project and an expiry time.
7. Save the automation.

## Review a run

Open the automation's run history to see its status and open the conversation for that run. From the list you can run it immediately, pause or resume the schedule, and edit or remove it.

## Keep schedules reliable

NusaShell must be running when an automation is due. If it was stopped, an overdue schedule runs once after NusaShell starts again, then returns to its normal schedule. A run that was interrupted while executing is recorded in the history; inspect its conversation before starting it again.

For command-line scheduling and cron syntax, see [the automation section of the CLI guide](../cli-guide.md#nusashell-automation).
