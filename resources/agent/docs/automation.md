# Automation and pipelines

NusaShell embeds a local CI runner and an automation engine in the same Go process. Humans use the **Automation** sidebar view. The agent uses structured tools. Do not invent shell `sleep` loops or scrape the UI for job IDs.

## Repository verification before push

For source changes, the repository provides one local quality gate:
`make verify-local`. It checks formatting, UI documentation drift, `go vet`,
native Go tests (with `-race` when the local C compiler is available), the Go
build, frontend tests, and compile-only test/build targets for Windows and
macOS. Compile-only checks catch source portability but cannot execute a test
binary on a different kernel; the GitHub Actions matrix remains the runtime
check for all three operating systems.

Run `make hooks` once per clone to enable `.githooks/pre-push`, which invokes
the same gate before a push. The hook is opt-in because Git hook configuration
is local to each clone. It changes that clone’s repository-local
`core.hooksPath`; preserve or chain any global hooks before enabling it.

Good:

    make verify-local
    make hooks

Bad:

    go test ./...       # skips formatting, vet, frontend, and other OS compile checks
    git push            # pushes before the repository gate has run

## Pipeline files

Pipeline definitions live as YAML files under `<data-dir>/ci/pipelines/<name>.yaml`. NusaShell discovers them on boot, parses each into a workflow, and registers it in the automation registry (`ci/automation.db`). Triggers in pipeline files are activated immediately — there is no separate "register" step.

The file name (without `.yaml`) becomes the workflow ID. For example `deploy.yaml` → workflow ID `deploy`. Edit the file with any text editor; restart NusaShell to pick up changes.

Pipeline files use the same YAML schema as `automation_create` — `name`, `triggers`, `jobs`, `env`, `defaults`, etc. Jobs form a DAG via `needs`. Independent jobs run in parallel on the local executor (this machine, host access).

## Saved automations

`automation_create` / `automation.save` persist a workflow in `ci/automation.db` (not conversation JSON). They are the same concept as pipeline files, just created via the agent or UI instead of a file on disk. Triggers:

- **once** — RFC3339 timestamp. Fires at most once. Survives restart.
- **every** — `cron` (5-field calendar) or `interval` (elapsed duration such as `1h`). They are not equivalent.
- **when** — event type plus optional `where` filters. Duplicate `(event, trigger, workflow)` deliveries are ignored.
- **manual** — UI, RPC, or `automation.run` only.

Availability is `runnable`, `blocked`, `disabled`, or `invalid`. Blocked means a required MCP provider is disabled or not running. Enable the provider and turn on **Auto start** in Plugins if the workflow should have that MCP ready as soon as NusaShell boots; do not rewrite the YAML.

## Plugin push events (server→client notifications)

Message-bridge plugins (e.g. `nusashell.telegram`) can **push** events to the
host instead of being polled. The plugin's ingester sends an MCP
`notifications/message` notification right after an inbound message is stored;
the host bridges it (`infrastructure/mcpclient/notify.go`) into a domain event
of type `<short-plugin-id>.<event>` (e.g. `telegram.message`) with attributes
`chat_id`, `chat_type`, `message_id`, `subject`, `text`, `from_me` (always
false — outbound messages never push).

Use **`when`** with these events to react instantly and avoid polling/LLM
spawns when nothing is new:

Good example:

    automation_create(yaml=
      version: 1
      name: Telegram auto-reply
      triggers:
        - when:
            event: telegram.message
            where:
              chat_type: dm
      jobs:
        respond:
          steps:
            - name: Balas DM
              agent:
                prompt: "…balas DM terbaru via nusashell.telegram…")

Bad examples:

    automation_create(yaml="…triggers: [every: {interval: 30s}]…")   # polls forever, spawns a turn even when idle
    # an event name that no plugin pushes, e.g. when: {event: email.received} with no email plugin — never fires

The event is delivered exactly once per `(chat_id, message_id)` (`RecordDelivery`
dedups), survives host restarts (the event store is durable), and the workflow
starts with the agent step able to read the stored message back via the plugin's
read tools. Disabled-event latency is zero — the notification fires the moment
ingestion finishes.

`invalid` means the YAML failed to parse or failed syntax/capability validation. The workflow stays listed. It cannot be enabled or run until the YAML is fixed. Unparseable pipeline files under `<data-dir>/ci/pipelines/` appear the same way — they are not skipped.

Good example:

    automation_list()                         # → [{id: "broken", availability: "invalid", reason: "yaml: ..."}, ...]
    # fix the YAML, then:
    automation_enable(id="broken")

Bad examples:

    automation_enable(id="broken")            # rejected while availability is invalid
    ci_run(workflow_id="broken")              # invalid YAML cannot start a run

## Waiting

A step may set `wait_until: <RFC3339>`. The run status becomes `waiting` and the executor is released. After restart, due waits resume automatically.

## Agent tools

| Tool | Use |
| --- | --- |
| `ci_run` / `ci_run_status` / `ci_logs` / `ci_cancel` | Start and observe execution by `workflow_id` |
| `automation_list` / `automation_read` / `automation_validate` / `automation_create` | Saved workflows and pipeline files |
| `automation_enable` / `automation_disable` / `automation_status` | Lifecycle |
| `schedule_once` / `schedule_every` | Create durable schedules (NusaShell owns the timer) |
| `wait_until` | How to park a run without occupying a runner |

After `ci_run`, call `ci_run_status`. Fetch logs only for failed jobs.

### ci_run async workflow

For a long pipeline, pass `async: true` so `ci_run` returns a `run_id`
immediately, continue other work, then `ci_wait` once for completion.

Good example:

    automation_list()                                    # → [{id: "deploy", ...}, ...]
    ci_run(workflow_id="deploy", async=true)             # → {run_id: "run_42", status: "queued"}
    # … do other work …
    ci_wait(run_id="run_42", timeout_ms=300000)          # blocks until terminal or timeout
    ci_logs(job_id="run_42:job_1")                       # only if a job failed

Bad examples:

    ci_run(workflow_id="deploy")                         # blocks the turn for the whole pipeline

    ci_run_status(run_id="run_42")                       # polled in a sleep loop
    sleep(seconds=5)                                     # instead of a single ci_wait

Pipeline `agent:` steps run a full agent turn (tool loop, compaction, skills,
memory, docs) synchronously via `RunHeadlessTurn`. They persist a conversation
so `ci_steer` can address the running step, but that conversation is not an
Agent room: it is omitted from `agent.conversations.list` and the Rooms pane.
They do not receive ACP tools (`subagent`, `subagent_steer`, `subagent_stop`,
`subagent_wait`) — permission prompts are interactive and must not stall an
unattended run. The composer agent still sees those tools when an ACP provider
is enabled.

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

Open **Automation** in the sidebar. Tabs: Workflows, Runs, Schedules, Events. New automation opens a once/every/when/manual wizard. Pipeline files from `<data-dir>/ci/pipelines/` appear alongside saved automations in the Workflows tab. Blocked automations show Enable provider. Running agent steps show a **Steer** button.
