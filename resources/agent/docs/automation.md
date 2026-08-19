# Automation and pipelines

NusaShell embeds a local CI runner and an automation engine in the same Go process. Humans use the **Automation** sidebar view. The agent uses structured tools. Do not invent shell `sleep` loops or scrape the UI for job IDs.

## Workspace pipeline

A repository may include `.nusashell/pipeline.yaml`. `ci_pipeline_read` / `ci.pipelines.read` load that file. `ci_run` with a `workspace` starts it. Jobs form a DAG via `needs`. Independent jobs run in parallel on the local executor (this machine, host access).

## Saved automations

`automation_create` / `automation.save` persist a workflow in `ci/automation.db` (not conversation JSON). Triggers:

- **once** — RFC3339 timestamp. Fires at most once. Survives restart.
- **every** — `cron` (5-field calendar) or `interval` (elapsed duration such as `1h`). They are not equivalent.
- **when** — event type plus optional `where` filters. Duplicate `(event, trigger, workflow)` deliveries are ignored.
- **manual** — UI, RPC, or `automation.run` only.

Availability is `runnable`, `blocked`, `disabled`, or `invalid`. Blocked means a required MCP provider is disabled or not running. Enable the provider and turn on **Auto start** in Plugins if the workflow should have that MCP ready as soon as NusaShell boots; do not rewrite the YAML.

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

### ci_run async workflow

For a long pipeline, pass `async: true` so `ci_run` returns a `run_id`
immediately, continue other work, then `ci_wait` once for completion.

Good example:

    ci_run(workspace="/home/user/proj", async=true)      # → {run_id: "run_42", status: "queued"}
    # … do other work …
    ci_wait(run_id="run_42", timeout_ms=300000)          # blocks until terminal or timeout
    ci_logs(job_id="run_42:job_1")                       # only if a job failed

Bad examples:

    ci_run(workspace="/home/user/proj")                  # blocks the turn for the whole pipeline

    ci_run_status(run_id="run_42")                       # polled in a sleep loop
    sleep(seconds=5)                                     # instead of a single ci_wait

Pipeline `agent:` steps run a full agent turn (tool loop, compaction, skills,
memory, docs) synchronously via `RunHeadlessTurn`. They do not receive ACP
tools (`subagent`, `subagent_steer`, `subagent_stop`, `subagent_wait`) —
permission prompts are interactive and must not stall an unattended run. The
composer agent still sees those tools when an ACP provider is enabled.

An `agent:` step accepts an optional `model` field (`provider_id:model_id` or
bare model ID). When omitted, the first enabled provider's first model is
used. The step output is `{"output": "<final assistant text>"}`.

A running agent step can be steered (additional instructions queued without
canceling) via `ci.runs.steer` RPC or the `ci_steer` agent tool. The steer
text is injected at the next tool-round boundary.

## Webhooks

A workflow can set `webhook_url` at the top level. When a run completes or
fails, NusaShell POSTs a JSON payload (`run_id`, `workflow`, `status`,
`started_at`, `finished_at`, `jobs`, `failed`, `success`) to that URL. The
webhook is fire-and-forget (10s timeout, errors logged as `ci.webhook.failed`
events, never blocks the run).

## UI

Open **Automation** in the sidebar. Tabs: Workflows, Runs, Schedules, Events. New automation opens a once/every/when/manual wizard. Run pipeline starts the workspace YAML. Blocked automations show Enable provider. Running agent steps show a **Steer** button.
