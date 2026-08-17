# Automation and pipelines

NusaShell embeds a local CI runner and an automation engine in the same Go process. Humans use the **Automation** sidebar view. The agent uses structured tools. Do not invent shell `sleep` loops or scrape the UI for job IDs.

## Workspace pipeline

A repository may include `.nusashell/pipeline.yaml`. `ci_pipeline_read` / `ci.pipelines.read` load that file. `ci_run` with a `workspace` starts it. Jobs form a DAG via `needs`. Independent jobs run in parallel on the local executor (this machine, host access).

## Saved automations

`automation_create` / `automation.save` persist a workflow in `automation.db` (not conversation JSON). Triggers:

- **once** — RFC3339 timestamp. Fires at most once. Survives restart.
- **every** — `cron` (5-field calendar) or `interval` (elapsed duration such as `1h`). They are not equivalent.
- **when** — event type plus optional `where` filters. Duplicate `(event, trigger, workflow)` deliveries are ignored.
- **manual** — UI, RPC, or `automation.run` only.

Availability is `runnable`, `blocked`, `disabled`, or `invalid`. Blocked means a required MCP provider is disabled or not running. Enable the provider; do not rewrite the YAML.

## Waiting

A step may set `wait_until: <RFC3339>`. The run status becomes `waiting` and the executor is released. After restart, due waits resume automatically.

## Agent tools

| Tool | Use |
| --- | --- |
| `ci_pipeline_list` / `ci_pipeline_read` / `ci_pipeline_validate` | Workspace `.nusashell/pipeline.yaml` |
| `ci_run` / `ci_run_status` / `ci_logs` / `ci_cancel` | Start and observe execution |
| `automation_list` / `automation_read` / `automation_validate` / `automation_create` | Saved workflows |
| `automation_enable` / `automation_disable` / `automation_status` | Lifecycle |
| `schedule_once` / `schedule_every` | Create durable schedules (NusaShell owns the timer) |
| `wait_until` | How to park a run without occupying a runner |

After `ci_run`, call `ci_run_status`. Fetch logs only for failed jobs.

## UI

Open **Automation** in the sidebar. Tabs: Workflows, Runs, Schedules, Events. New automation opens a once/every/when/manual wizard. Run pipeline starts the workspace YAML. Blocked automations show Enable provider.
