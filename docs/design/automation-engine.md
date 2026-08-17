# NusaShell Automation Engine — Scheduling, Events, and Dynamic Workflows

Status: Proposed extension to `docs/design/ci-runner.md`
Target: NusaShell Light / `jahrulnr/NusaShell-agent`

## 1. Important architectural correction

The CI Runner design is necessary but not sufficient for the broader use cases intended for NusaShell.

A CI runner answers:

> "When a pipeline has been triggered, how do we execute its jobs?"

NusaShell also needs to answer:

> "What causes a workflow to start, when should it start, what conditions should be evaluated, and what should happen when an external event arrives?"

Therefore NusaShell should not make the scheduler itself responsible for all automation semantics. Add an **Automation Engine** above the existing Pipeline/DAG/Runner system.

The resulting architecture is:

```text
                         Automation Definition
                                  |
                    +-------------+-------------+
                    |                           |
               Triggers                     Workflow/DAG
                    |                           |
       +------------+------------+              |
       |            |            |              |
     once         every         when            |
       |            |            |              |
    Schedule      Schedule     Event Bus        |
       |            |            |              |
       +------------+------------+--------------+
                                  |
                           Trigger Engine
                                  |
                           create Run
                                  |
                            CI Scheduler
                                  |
                         +--------+--------+
                         |                 |
                    Local Runner     Docker Runner
                         |                 |
                         +--------+--------+
                                  |
                     logs / artifacts / state
                                  |
                         Agent + Human UI
```

The CI Runner becomes the execution subsystem. The Automation Engine becomes the trigger/orchestration subsystem.

## 2. Why GitHub Actions and GitLab are the right references

The original decision to study GitHub Actions and GitLab is correct because their strength is not merely command execution. They provide a mature event-to-workflow model.

GitLab pipelines can be started by code events, merge requests, schedules, or manually. Scheduled pipelines are independent of code changes and use cron-based schedules. GitLab also exposes pipeline schedules through an API, including cron, timezone, active state, target ref, and inputs. citeturn0search8turn0search0turn0search3

GitLab `rules` are also important because they separate the fact that a pipeline exists from whether an individual job should be included. Rules can use conditions such as `if`, `changes`, `exists`, and `when`, and can express manual, delayed, failure, success, and always-run behavior. citeturn0search2turn0search9

GitLab also supports external pipeline triggers through APIs and downstream pipelines. This establishes a useful model for NusaShell: external systems should be able to emit a structured event that starts or advances a workflow. citeturn0search6

GitHub's ecosystem goes even further conceptually. Dependabot is a useful example because it is not merely a scheduled shell command. It monitors dependency state, produces security/version-update events, and can create automated pull requests. Dependabot version updates have configurable schedules, while security alerts react to newly discovered vulnerabilities or dependency graph changes. citeturn0search17turn0search1turn0search13

The lesson for NusaShell is therefore:

**Do not design a timer feature. Design an automation trigger engine. Timers are only one trigger type.**

## 3. Trigger model

NusaShell should support three first-class trigger families matching the requested mental model:

```text
once   = one-shot time trigger
 every = recurring time trigger
 when  = event/condition trigger
```

User-facing UI should literally expose these three choices because they are easier to understand than cron/event terminology.

Internally, normalize them into:

```go
type Trigger interface {
    Type() TriggerType
    Next(now time.Time) *time.Time
    Match(event Event) bool
}
```

Trigger types:

```text
schedule.once
schedule.interval
schedule.cron
event.webhook
event.email
event.file
event.git
event.system
event.timer
event.pipeline
condition
```

`when` does not mean only webhook. It means a workflow becomes eligible when an event matches a condition.

## 4. `once`

Example:

```yaml
triggers:
  - once:
      at: 2026-08-18T09:00:00+07:00
```

Use cases:

- send an email tomorrow at 09:00
- restart a service at 02:00 once
- remind the user about an expiring certificate
- perform a one-time migration
- execute an agent workflow at a specific future time

Important implementation detail: a `once` trigger is a persisted database record, not an in-memory `time.After` or goroutine sleep.

NusaShell may be restarted, upgraded, or crash before the scheduled time. The trigger must survive process restarts.

The database stores:

```text
trigger_id
workflow_id
kind = once
run_at
timezone
status = pending|fired|cancelled|expired
created_at
fired_at
```

## 5. `every`

Examples:

```yaml
triggers:
  - every:
      cron: "0 12 * * *"
      timezone: "Asia/Jakarta"
```

or:

```yaml
triggers:
  - every:
      interval: 1h
```

The distinction is useful:

`cron` means calendar semantics: "at 12:00 every day".

`interval` means elapsed-time semantics: "every 60 minutes".

Do not silently treat these as equivalent.

For calendar schedules, use an IANA timezone. Never store only a UTC offset because DST/timezone rules may matter for users and future portability.

GitLab's current scheduled pipeline model explicitly supports cron schedules and cron timezones, which is a strong precedent for NusaShell's `every` implementation. citeturn0search0turn0search3

## 6. `when`

`when` is the important part for the broader NusaShell vision.

Examples:

```yaml
triggers:
  - when:
      event: email.received
      where:
        mailbox: work
        from: "alerts@example.com"
```

or:

```yaml
triggers:
  - when:
      event: vps.health_changed
      where:
        status: unhealthy
```

or:

```yaml
triggers:
  - when:
      event: git.push
      where:
        branch: main
```

The event itself should be normalized into a common envelope:

```go
type Event struct {
    ID         string
    Type       string
    Source     string
    Time       time.Time
    Subject    string
    Attributes map[string]any
    Data       json.RawMessage
}
```

The trigger matcher evaluates the event against the workflow trigger without exposing the whole event payload to the agent unless required.

## 7. Event sources

NusaShell should not hard-code every external service into the Automation Engine.

Instead, define an event-source interface:

```go
type EventSource interface {
    Name() string
    Start(ctx context.Context, sink EventSink) error
    Stop(ctx context.Context) error
}
```

Possible event sources:

```text
GitHub webhook
GitLab webhook
Email connector
IMAP/POP polling connector
HTTP webhook
filesystem watcher
cron/timer
NusaShell internal events
MCP event source
plugin event source
```

This is especially important for the existing NusaShell plugin/MCP architecture. External integrations should be adapters that publish normalized events. The Automation Engine should not know whether an event originated from Gmail, Outlook, GitHub, a filesystem, or an MCP server.

## 8. Email example

The user's example:

> "If an email comes in, read it and perform an action."

should become:

```text
Email provider
      |
      | new message
      v
Email Event Source
      |
      v
Event Bus
      |
      v
Trigger Matcher
      |
      | matches email.received
      v
Create Workflow Run
      |
      v
Job: Read email
      |
      v
Condition / Agent decision
      |
      +---- no -> finish
      |
      +---- yes -> action job
```

Example definition:

```yaml
name: Process billing email

triggers:
  - when:
      event: email.received
      where:
        mailbox: finance
        subject_contains: "invoice"

jobs:
  inspect:
    steps:
      - name: Read message
        uses: email.read

  decide:
    needs: [inspect]
    agent:
      prompt: |
        Determine whether this invoice requires payment approval.
        Return approve or ignore.

  approve:
    needs: [decide]
    if: decision == "approve"
    steps:
      - uses: email.send
        with:
          to: finance@example.com
          template: payment-approval
```

The exact `agent` and `uses` syntax should be finalized separately. The architectural point is that event reception and action execution are separate concerns.

## 9. Delayed execution

The example:

> "Kirim email besok jam 9 pagi."

is not necessarily a recurring pipeline. It is a workflow containing a durable delay.

Support a first-class `delay`/`wait_until` operation:

```yaml
jobs:
  reminder:
    steps:
      - wait_until: 2026-08-18T09:00:00+07:00
      - uses: email.send
        with:
          to: user@example.com
          subject: Reminder
```

However, do not keep a runner process alive while waiting.

The execution state should transition to:

```text
waiting
```

and persist a wake-up record:

```text
workflow_run_id
job_id
step_id
wake_at
timezone
status = pending
```

The Automation Engine wakes the workflow at the requested time and resumes the job.

This is fundamentally different from a normal CI job and is why the Automation Engine must sit above the runner.

## 10. Event-driven versus scheduled execution

NusaShell should have two paths into the same Run system:

```text
Schedule -------------------+
                             |
Webhook --------------------+
                             |
Email ----------------------+
                             |
Git event ------------------+--> Trigger Engine --> Run
                             |
Internal event -------------+
                             |
Manual UI/Agent ------------+
```

After the run is created, all paths use the same DAG scheduler and runner infrastructure.

This is the key convergence point between GitHub Actions/GitLab CI and broader personal automation.

## 11. Conditions are separate from triggers

Do not make `when` rules and job `if` rules the same mechanism.

Trigger conditions answer:

> Should this workflow start?

Job conditions answer:

> Given that the workflow is running, should this job execute?

Example:

```yaml
triggers:
  - when:
      event: email.received
      where:
        mailbox: work

jobs:
  classify:
    ...

  notify:
    needs: [classify]
    if: event.subject_contains("urgent")
```

This distinction maps closely to GitLab's separation between pipeline creation/rules and job-level rules. GitLab evaluates `rules` when creating the pipeline and can include/exclude jobs based on conditions. citeturn0search2turn0search9

## 12. Dynamic workflow model

The pipeline definition should not be considered a static CI-only file.

A NusaShell automation can be:

```text
scheduled
triggered by event
manually started
started by another workflow
started by an agent
resumed after waiting
```

Therefore rename the internal concept from only `PipelineDefinition` toward:

```text
WorkflowDefinition
WorkflowRun
```

The UI can still call the feature `Pipelines` initially because the CI mental model is familiar. Internally, the broader abstraction should be workflow-oriented.

Recommended compatibility layer:

```go
type WorkflowDefinition struct {
    ID        string
    Name      string
    Triggers  []Trigger
    Jobs      []Job
}

type PipelineDefinition = WorkflowDefinition
```

If the project is still early enough, simply rename the domain model before implementation starts rather than maintaining two parallel concepts.

## 13. Actions / built-in capabilities

This is where GitHub Actions provides another useful idea.

A workflow should not require every operation to be a raw shell command.

Use an action abstraction:

```yaml
steps:
  - uses: email.read
  - uses: http.request
  - uses: filesystem.read
  - uses: filesystem.write
  - uses: github.issue.create
  - uses: docker.run
```

An action is a structured operation with an input/output schema.

Conceptually:

```go
type Action interface {
    Name() string
    InputSchema() Schema
    OutputSchema() Schema
    Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}
```

Shell commands remain supported:

```yaml
- run: go test ./...
```

but structured actions are preferable when the operation has an API-level meaning.

This is particularly important for agent-generated workflows. An agent can reliably generate:

```yaml
- uses: email.send
  with:
    to: ...
```

rather than constructing provider-specific shell commands.

## 14. Action versus MCP tool

Do not duplicate every MCP tool as a pipeline action.

Use this distinction:

`Action` = deterministic workflow capability intended to be called by an automation definition.

`MCP tool` = model-facing capability exposed to an agent at runtime.

An action may internally call an MCP-backed integration, but the workflow contract should remain stable.

Example:

```text
Workflow
   |
email.send action
   |
Email integration adapter
   |
provider API
```

The agent may also have:

```text
email_send MCP/built-in tool
```

Both can share the same underlying application service.

## 15. Agent interaction with automation

Add an automation toolbox in addition to the CI tools.

Recommended tools:

`automation_list`

`automation_read`

`automation_validate`

`automation_create`

`automation_update`

`automation_enable`

`automation_disable`

`automation_run`

`automation_cancel`

`automation_status`

`automation_events`

`schedule_once`

`schedule_every`

`wait_until`

The agent should be able to translate natural language into these structured operations.

Example:

> "Setiap jam 12 pantau VPS, kalau down kirim Telegram."

The agent should construct a workflow roughly equivalent to:

```yaml
triggers:
  - every:
      cron: "0 12 * * *"
      timezone: Asia/Jakarta

jobs:
  check:
    steps:
      - uses: vps.health.check

  notify:
    needs: [check]
    if: check.status != "healthy"
    steps:
      - uses: telegram.send
```

The model should not be responsible for maintaining timers itself. NusaShell owns durable scheduling.

## 16. Agent as workflow step

A major NusaShell differentiator can be an agent step:

```yaml
- agent:
    prompt: |
      Analyze the VPS health result.
      If the service is unhealthy, explain the likely cause.
```

The agent step should execute through the existing NusaShell agent runtime rather than a separate AI subsystem.

The result must be structured when downstream logic depends on it:

```yaml
- agent:
    prompt: ...
    output_schema:
      action:
        enum: [notify, ignore, restart]
```

Then:

```yaml
- if: agent.action == "restart"
  uses: vps.restart
```

This allows NusaShell to move beyond conventional CI into agentic automation while retaining deterministic orchestration around the model.

## 17. Dynamic branching

A static DAG is insufficient for highly dynamic agent workflows.

Support two levels:

Level 1 — static DAG with runtime conditions:

```yaml
if: result.status == "failed"
```

Level 2 — bounded dynamic fan-out:

```yaml
for_each: servers
```

Example:

```yaml
jobs:
  inspect:
    steps:
      - uses: vps.list

  health:
    needs: [inspect]
    for_each: inspect.servers
    steps:
      - uses: vps.health.check
```

Do not initially support arbitrary graph mutation by scripts. Dynamic fan-out should have explicit limits to prevent accidental runaway execution.

## 18. Run concurrency and deduplication

Event-driven automation creates a new problem that normal CI only partially solves: multiple events can arrive while a previous workflow is still running.

Every workflow should support a concurrency policy:

```yaml
concurrency:
  key: vps-monitor
  policy: replace
```

Policies:

```text
allow       = every event creates a run
queue       = serialize runs
replace     = cancel old pending/running run and keep newest
skip        = ignore event while another run is active
```

This is essential for webhooks, email bursts, file watchers and polling sources.

## 19. Debounce and rate limits

Event sources should optionally support debounce:

```yaml
triggers:
  - when:
      event: filesystem.changed
    debounce: 30s
```

This prevents hundreds of events from creating hundreds of runs during operations such as `git checkout`, dependency installation, or large file copies.

The event engine should also enforce global and per-workflow limits:

```text
max events/sec
max concurrent workflow runs
max queued runs
max dynamic jobs/run
```

## 20. Polling sources

Not every integration provides webhooks.

For example, an email provider may require polling.

A polling event source should store a cursor/checkpoint:

```text
source_id
cursor
last_poll_at
last_success_at
```

The source converts newly discovered records into events and advances the cursor only after events are durably accepted.

This gives at-least-once delivery semantics.

The Automation Engine must therefore make event processing idempotent.

## 21. Event delivery semantics

Initial guarantee:

**at-least-once event delivery + idempotent trigger handling.**

Do not promise exactly-once execution. It is much harder and usually unnecessary for personal automation.

Each event has an ID. A trigger execution records:

```text
event_id
workflow_id
trigger_id
matched_at
run_id
```

A unique constraint on `(event_id, trigger_id, workflow_id)` prevents duplicate workflow creation when the same event is delivered twice.

## 22. Scheduler architecture

There are now two schedulers with different responsibilities.

`AutomationScheduler`:

- wakes time-based triggers
- manages delayed steps
- consumes external events
- matches triggers
- creates workflow runs
- handles debounce/concurrency

`ExecutionScheduler`:

- evaluates job dependencies
- matches runners
- starts jobs
- tracks execution
- manages cancellation/retry

Do not merge them into one giant scheduler.

```text
AutomationScheduler
       |
       v
 WorkflowRun
       |
       v
ExecutionScheduler
       |
       v
Runner/Executor
```

## 23. Durable timer implementation

A simple implementation can use SQLite as the durable schedule store.

Tables:

```text
automation_workflows
automation_triggers
automation_schedules
automation_events
automation_event_deliveries
automation_waits
automation_runs
automation_run_locks
```

Indexes:

```text
automation_schedules(status, next_run_at)
automation_events(created_at)
automation_event_deliveries(event_id, trigger_id)
automation_waits(status, wake_at)
automation_runs(workflow_id, created_at DESC)
```

The timer loop should query the nearest due timestamp rather than waking every second.

Conceptually:

```text
now = current time
next = SELECT MIN(next_run_at) WHERE status = pending
sleep until next or event notification
claim due records transactionally
create workflow runs
calculate next occurrence
commit
```

On process restart, the loop simply queries the database again. No timer state is lost.

## 24. Time zones and missed schedules

Schedules need an explicit policy for missed executions.

Example: NusaShell is shut down at 11:59 and restarted at 13:00.

For:

```yaml
cron: "0 12 * * *"
```

choose a policy:

```text
skip_missed
run_once_after_restart
catch_up_all
```

Default should be `skip_missed` for recurring monitoring to avoid a burst of stale executions.

For one-shot reminders, default should be `run_once_after_restart` if the scheduled time has passed but the reminder has not fired.

## 25. UI/UX: Automation center

The existing Pipelines screen should evolve into an **Automation** center.

Primary sections:

```text
Automation
  Workflows
  Runs
  Schedules
  Events
  Runners
  Artifacts
```

The first screen should show active automations rather than only completed CI runs.

Example:

```text
Automation

ACTIVE

VPS monitor                         Every day 12:00
● Next run in 47 min                Enabled

Invoice email processor             When email arrives
● Listening                         Enabled

Tomorrow reminder                   Once · Aug 18 09:00
● Scheduled                         Enabled

────────────────────────────────────────────
Recent activity
✓ VPS monitor       12:00    healthy
✓ Invoice processor 11:42    ignored
✕ Deploy workflow  10:18    failed
```

This is much closer to the user's actual mental model than a traditional CI dashboard.

## 26. Workflow builder UX

Do not force users to write YAML for common automation.

The create workflow UI should start with:

```text
What should start this automation?

( ) Once
( ) Every
( ) When something happens
( ) Manually
```

Once:

```text
Date       [18 Aug 2026]
Time       [09:00]
Timezone   [Asia/Jakarta]
```

Every:

```text
Frequency  [Every day ▼]
Time       [12:00]
Timezone   [Asia/Jakarta]
```

When:

```text
Event      [Email received ▼]
Filter     [Mailbox = Finance]
            [Subject contains invoice]
```

Then show:

```text
WHEN
  ↓
DO
  ↓
IF
  ↓
DO
```

The visual builder should generate the canonical workflow definition rather than creating a separate configuration format.

## 27. Run detail UX for waiting states

A workflow run must support a state that is not simply running or queued.

Example:

```text
Invoice reminder

● Waiting until tomorrow 09:00

Trigger
✓ User requested reminder

Execution
○ Wait until 18 Aug 09:00
○ Send email
```

For event waits:

```text
● Waiting for: email.received

Filter:
mailbox = finance
subject contains = invoice
```

This makes long-running automation understandable to humans.

## 28. Agent-facing automation status

Agent tools must distinguish:

```text
scheduled
waiting
running
completed
failed
cancelled
```

Example compact result:

```json
{
  "workflow_id": "invoice-reminder",
  "status": "waiting",
  "wake_at": "2026-08-18T09:00:00+07:00",
  "next_action": "email.send"
}
```

The agent does not need to remain active while the workflow waits.

This is critical for context and resource efficiency.

## 29. Security implications

Event-driven automation expands the attack surface substantially compared with CI.

An incoming webhook, email, file event, or external API response can cause code execution. Therefore event sources must not automatically grant arbitrary shell execution without an explicit workflow authorization.

Each workflow should declare a trust level:

```text
safe
trusted
privileged
```

Actions should also declare capabilities:

```text
email.read
email.send
filesystem.read
filesystem.write
shell.execute
network.request
vps.restart
secret.read
```

Before enabling a workflow, NusaShell should be able to display:

```text
This automation can:
• read email
• send email
• execute shell commands
• access credentials
```

The user can then approve the workflow.

## 30. GitHub/Dependabot-style dynamic automation

The goal should not be "NusaShell CI" in the narrow sense.

The more useful target is the design pattern behind GitHub's ecosystem:

```text
observe state
    ↓
detect change
    ↓
create event
    ↓
apply rules
    ↓
perform workflow
    ↓
produce artifact/action
    ↓
notify or create follow-up work
```

Dependabot is a good conceptual example. It observes dependency/security state, reacts to changes or schedules, determines whether action is required, and can create/update pull requests. GitHub documents security alerts reacting to newly discovered vulnerabilities or dependency graph changes, and version updates running on configurable schedules. citeturn0search1turn0search17turn0search13

NusaShell can generalize this pattern beyond software dependencies:

```text
VPS monitor
Email assistant
Backup monitor
Certificate expiry monitor
Git repository maintenance
Website uptime monitor
Invoice processor
Database health check
Personal reminders
Scheduled agent research
File watcher automation
MCP-driven integrations
```

## 31. Recommended built-in action categories

Do not implement dozens of providers directly in the Automation Engine.

Create stable application services around categories:

```text
email.*
http.*
filesystem.*
git.*
process.*
vps.*
notification.*
workflow.*
agent.*
```

Providers then implement these capabilities.

For example:

```text
email.send
   |
   +-- Gmail adapter
   +-- Microsoft Graph adapter
   +-- SMTP adapter
```

The workflow does not care which provider is used.

## 32. Relationship to MCP

MCP should remain an integration surface, not the scheduler.

An MCP server can expose tools such as:

```text
email.read
email.send
vps.health
vps.restart
```

NusaShell can adapt selected MCP tools into actions when their schemas and trust model are suitable.

Conversely, NusaShell's Automation Engine can expose workflow state through MCP if future integrations need it.

The scheduler itself must remain independent from MCP so automation continues to work if an MCP server is unavailable.

## 33. Relationship to GitHub Actions and GitLab CI

The architecture should now be viewed as:

```text
GitHub Actions / GitLab concepts
              |
              v
       NusaShell Workflow
              |
      +-------+-------+
      |               |
   Triggers        Execution
      |               |
 schedule/event    DAG/jobs
      |               |
      +-------+-------+
              |
           Runner
```

GitHub/GitLab are primarily optimized for repository-centric automation. NusaShell should be optimized for **agent-centric personal automation**, while retaining the same powerful primitives.

That makes the original choice of GitHub/GitLab as architectural references stronger, not weaker.

## 34. Revised implementation phases

### Phase A — CI execution core

Implement the existing CI Runner design:

- workflow/pipeline parser
- DAG
- jobs/steps
- local executor
- logs
- artifacts
- cache
- runner registry
- agent tools

### Phase B — durable scheduling

Add:

- `once`
- `every` cron
- `every` interval
- timezone
- schedule persistence
- missed-run policy
- scheduler recovery

### Phase C — event engine

Add:

- normalized event envelope
- event bus integration
- `when`
- event filters
- event deduplication
- debounce
- concurrency policies

### Phase D — wait/resume

Add:

- `wait_until`
- event wait
- durable workflow suspension
- resume after restart

### Phase E — structured actions

Add:

- action registry
- action schemas
- deterministic action execution
- provider adapters
- agent action step

### Phase F — visual Automation UI

Add:

- workflow list
- create wizard
- schedules
- event listeners
- run detail
- waiting states
- action permissions

### Phase G — advanced agentic workflows

Add only after the deterministic foundation is stable:

- agent steps
- structured model outputs
- bounded dynamic fan-out
- dynamic conditions
- automated diagnosis
- self-healing workflows

## 35. Final architecture decision

NusaShell should evolve from a **CI Runner** into an **Automation Engine with a CI-compatible execution core**.

The execution layer remains:

```text
Workflow -> DAG -> Job -> Step -> Runner -> Executor
```

The new trigger layer becomes:

```text
Once
Every
When
Manual
```

and all triggers converge into the same `WorkflowRun` system.

The critical architectural rule is:

**Timers, events, webhooks, email, filesystem changes, Git changes, agent requests, and manual UI actions are all trigger sources. They should never implement their own execution path. They create a normal WorkflowRun.**

Likewise, waiting should never keep a runner occupied. A waiting workflow is persisted and resumed later.

This gives NusaShell the flexibility that makes GitHub Actions/GitLab CI valuable while extending the model into personal agentic automation. The user can say "every day at 12", "tomorrow at 9", or "when an email arrives", and the natural-language request maps to durable workflow primitives rather than ad-hoc background goroutines.
