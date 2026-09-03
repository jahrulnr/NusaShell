# Automation and pipelines

NusaShell embeds a local-first automation engine. Humans use the
**Automation** view; agents use the dispatcher tools `automation` and
`automation_schedule`. A workflow is a durable YAML definition with a trigger,
one or more jobs, and sequential steps inside each job.

## Start with the builtin skill templates

The builtin `automation-authoring` skill is the cookbook for creating
workflows. Its templates are seeded under `<data-dir>/skills/` and are read as
support files; they are not executed automatically. Discover and read only the
template needed for the request:

    skill(op="search", query="automation authoring")
    file_list(path="<data-dir>/skills/automation-authoring/templates")
    file_read(path="<data-dir>/skills/automation-authoring/templates/telegram-auto-reply.yaml")

The package contains:

| Template | Use it for |
| --- | --- |
| `telegram-auto-reply.yaml` | one run per inbound `telegram.message` event |
| `alarm-once.yaml` | a one-shot local terminal alarm at an exact time |
| `reminder-every.yaml` | a recurring weekday reminder written to run output |

Copy the relevant YAML into `automation(op="validate")`, replace every
example value, then save it. A template is guidance, not a pipeline file; do
not pass its path to a shell command.

## Authoring workflow

1. Classify the request:
   - `when` for an external event pushed by a plugin;
   - `once` for one future RFC3339 time;
   - `every` for a cron calendar or elapsed interval;
   - `manual` when a human or agent starts it explicitly.
2. Keep one workflow focused on one outcome. Prefer a small DAG over a single
   giant agent prompt.
3. Read `yaml-contract.md` and `event-variables.md` from the skill when the
   fields or event payload are unfamiliar.
4. For an `agent:` step that needs an MCP action, discover the live plugin and
   schema before writing the prompt. Use `mcp_list`, `mcp_enable` once when
   stopped, `mcp_search` or `tool_list`, `tool_schema` when the argument shape
   is unclear, `contract_read` when the plugin declares a usage contract, and
   finally `mcp_call` with the exact returned `ref`. Do not invent a tool name
   from a template.
5. Validate the complete YAML before saving:

       automation(op="validate", yaml="...")

   Fix all `INVALID` issues and validate again. `BLOCKED` means a known MCP
   provider is stopped or disabled; enable that provider when the user wants
   the side effect.
6. Save new workflows disabled unless activation was explicitly requested:

       automation(op="create", yaml="...", enabled=false)

   The `enabled` argument is explicit because a create call otherwise enables
   the workflow. Read the saved definition and report its ID.
7. After confirmation, enable it. For a requested test run, use one async run
   and one wait; use status/logs only for diagnosis:

       automation(op="enable", workflow_id="wf_123")
       automation(op="run", workflow_id="wf_123", async=true)
       automation(op="wait", run_id="run_123", timeout_ms=300000)

Good:

    automation(op="validate", yaml="name: check\ntriggers:\n  - manual: true\njobs:\n  check:\n    steps:\n      - run: printf '%s\\n' ok")
    automation(op="create", yaml="...", enabled=false)
    automation(op="read", workflow_id="wf_123")

Bad:

    automation(op="run", workflow_id="check")  # blocks the turn when an async run was requested
    automation(op="status", run_id="run_123")  # repeated in a sleep/poll loop
    automation_schedule(op="every", interval="30s", yaml="...")  # polls for an event instead of using when
    automation(op="create", yaml="...", enabled=true)  # activates an unreviewed side effect

## Dispatcher contract

Each dispatcher is one provider-facing tool. `op` is required and is an enum;
unknown operations fail with the valid operation list. The root plus `op` is
the only tool identity.

`automation` supports:

| Operation | Required fields | Purpose |
| --- | --- | --- |
| `run` | `workflow_id` | Start a workflow; `async: true` returns immediately. |
| `wait` | `run_id` | Wait for a terminal snapshot; `timeout_ms` is bounded. |
| `status` | `run_id` | Return status, summary, and wake time. |
| `logs` | `job_id` | Read append-only log chunks using `after`/`limit`. |
| `cancel` | `run_id` | Cancel a run. |
| `steer` | `run_id`, `text` | Queue text for a running agent step. |
| `list` | — | List workflows with availability. |
| `read` | `workflow_id` | Read a workflow and capability bindings. |
| `validate` | `yaml` | Parse and capability-check YAML without saving. |
| `create` | `yaml` | Save a workflow; `name` and `enabled` are optional. |
| `enable` / `disable` | `workflow_id` | Toggle lifecycle. |
| `delete` | `workflow_id` | Remove a definition; existing run history remains. |

`automation_schedule` supports `op="once"` (`at`, `yaml`, optional `name`)
and `op="every"` (`cron` or `interval`, optional `timezone`, `yaml`, and
`name`). It creates a saved workflow and a durable schedule. Use the regular
`automation(op="create")` path when the YAML already contains its trigger.

## YAML shape

The parser accepts `version: 1` and the following top-level fields:
`name`, `enabled`, `trust`, `concurrency`, `missed`, `defaults`, `env`,
`webhook_url`, `triggers`, and `jobs`. The full field reference and examples
are in the skill's `references/yaml-contract.md`.

Every step chooses exactly one of:

- `run` — local shell command;
- `uses` — a builtin or registered automation capability with `with` input;
- `wait_until` — park the run until an RFC3339 time without occupying an
  executor;
- `agent` — a full headless NusaShell turn with optional `model` and
  `output_schema`.

Jobs are a DAG through `needs`; steps within one job remain sequential.
Independent jobs can run in parallel. Use `concurrency.policy` (`allow`,
`queue`, `replace`, or `skip`) when duplicate work would be harmful. Use
`retry` only for transient runner errors or timeouts, and make external write
actions idempotent before retrying them.

An `agent` step uses the same provider retry policy as an interactive
conversation, including transient connection resets, truncated streams, idle
timeouts, and eligible 5xx responses. Those retries are internal and do not
create a Learning Log retry button. Non-retryable errors (for example an
invalid request or a 429 without a usable `Retry-After`) fail the step; the
job-level `retry` setting is a separate workflow policy and does not replay an
agent step automatically.

## Event-driven workflows

Plugins can push notifications to the host. The host stores the event and
activates matching `when` triggers immediately; it does not poll the plugin.
Delivery is deduplicated by event ID, trigger ID, and workflow ID. A trigger's
`where` map performs cheap equality or `*_contains` matching before a run is
created.

Good:

    triggers:
      - when:
          event: telegram.message
          where: {chat_type: dm}
    jobs:
      reply:
        steps:
          - agent:
              prompt: "Reply to chat ${event.chat_id}, message ${event.message_id}: ${event.text}"

Bad:

    triggers:
      - every: {interval: 30s}  # creates turns even when no message arrived

### Supported event variables

Agent prompts may use `${event.<key>}`. The renderer supports these top-level
fields for every event:

| Variable | Meaning |
| --- | --- |
| `${event.type}` | normalized type, such as `telegram.message` |
| `${event.source}` | source/server identifier |
| `${event.subject}` | sender, chat label, or other display subject |

Event publishers can add direct or dotted attributes, which are also
available as `${event.<key>}` and `${event.<nested.path>}`. Missing values
render as an empty string; values are stringified. This syntax is prompt
rendering only — it is not shell expansion and does not expose `event.id` or
`event.time`.

For the Telegram notification bridge, the supported attributes are:

| Variable | Meaning |
| --- | --- |
| `${event.chat_id}` | destination chat identifier |
| `${event.message_id}` | source message identifier |
| `${event.chat_type}` | `dm`, `group`, `channel`, or empty |
| `${event.subject}` | sender/chat display label (falls back to `chat_id`) |
| `${event.text}` | truncated inbound text |
| `${event.from_me}` | whether the message came from the bot |

The bridge ignores `from_me: true`, so a bot reply cannot recursively trigger
the same workflow. An agent step should still guard on `from_me`, verify the
exact `chat_id` + `message_id` with a discovered read tool, and send at most
one reply. Do not use an unread-count query: reading a message can clear that
state before the event workflow runs.

### Shell environment

Shell steps receive merged workflow/job/step `env` plus these runtime values:

| Variable | Meaning |
| --- | --- |
| `NUSASHELL` | always `true` |
| `NUSASHELL_AUTOMATION` | always `true` |
| `NUSASHELL_PIPELINE_ID` | workflow ID |
| `NUSASHELL_RUN_ID` | current run ID |
| `NUSASHELL_JOB_ID` | current job ID |
| `NUSASHELL_STEP_ID` | current step ID |
| `NUSASHELL_WORKSPACE` | executor workspace path |

`${event.*}` is not automatically placed in shell environment. If a shell
action needs event data, prefer an `agent:` or structured `uses:` step instead
of concatenating untrusted text into a shell command.

## Telegram, alarm, and reminder guidance

The Telegram template uses an `agent:` step because MCP tool names differ by
installation. The agent must discover the plugin and call only the exact
`ref`; a successful run is not proof of delivery unless `mcp_call` returned a
successful result.

The alarm template is dependency-free: at the scheduled time it emits a
terminal bell and a message to the run log. Replace its `run` command with a
local desktop notifier only after checking that command on the target host.

The reminder template runs on a weekday cron and records a message in the run
log. To deliver it through Telegram or another channel, replace the shell step
with an `agent:` step that follows the live MCP discovery flow and fails
honestly when no notification channel is configured.

## Waiting, runs, logs, and steering

Use `wait_until` for a pause inside a workflow. The run becomes `waiting`, the
executor is released, and the due wait resumes after restart. Use one
`automation(op="wait")` call for an async run. Fetch logs for a failed or
diagnostic job; use `automation(op="steer")` only while an agent step is
running.

## Availability and safety

Workflow availability is `runnable`, `blocked`, `disabled`, or `invalid`.
Invalid YAML remains listed and cannot be enabled or run until fixed. A
blocked workflow has a missing/disabled provider; enable that provider instead
of hiding the failure. `trust: safe`, `trusted`, and `privileged` describe the
workflow's execution trust; choose the lowest level that permits the requested
action.

## Webhooks and storage

`webhook_url` receives a bounded completion/failure JSON summary with a
10-second timeout. Delivery failures are recorded as automation events and do
not block the run.

Definitions discovered from files live under
`<data-dir>/automation/pipelines/<name>.yaml`; saved definitions and run state
live in `<data-dir>/automation/workflows.db`. Restart after editing a pipeline
file so it is discovered again.

## Repository verification

For source changes, run `make verify-local`. It checks formatting, UI-doc
drift, Go vet/tests/race/build, frontend tests, and portability compile
targets. `make hooks` enables the same gate before a push.

## Product release streams

The repository CI has three independent product release streams. Go uses the
root `VERSION` and publishes the `go-v<VERSION>` release; Electron uses
`apps/electron/VERSION` and publishes the `electron-v<VERSION>` release; the
desktop pet uses `apps/pets/VERSION` and publishes the `pets-v<VERSION>`
release (Linux-only matrix). On a push to `master`, `detect-changes` marks a
stream when its product paths changed or when its VERSION is ahead of that
stream's pointer in `release-versions.json`. The latter is important when a
follow-up commit fixes tests or CI without touching a product path.

The Go release gate depends on frontend, backend, and installer tests. The
Electron release gate depends on wrapper, renderer, and installer tests. The
pets release gate depends on pets and installer tests. Do not infer that one
failed product test blocks another product's release. The release-index job
updates only publishers that actually succeeded and preserves the pointer
for a failed or skipped stream.

Good when diagnosing a release:

    gh run view <run-id> --json status,conclusion,jobs
    gh run view <run-id> --log-failed

Bad:

    gh run rerun <run-id> --failed  # reruns before identifying the failed stream
    git show release-versions.json  # treats the pointer file as the artifact itself
