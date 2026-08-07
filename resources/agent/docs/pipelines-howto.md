# Pipelines: Multi-step DAG orchestration

Pipelines chain multiple agent turns or tool calls into a directed acyclic
graph (DAG) with dependencies, conditional branching, and context passing.

**Beta status:** Pipelines support **manual run**, **event**, and **schedule**
triggers while NusaShell is open. Steps always execute **sequentially** in
topological order (`dependsOn` is ordering only, not parallel workers).

## When to use a pipeline vs a job

- **Job** — single action on a schedule or event.
- **Pipeline** — multiple steps that depend on each other, branch on
  conditions, or pass results between steps (manual, event, or schedule).

## Creating a pipeline

Event-triggered:

```
pipeline action=add name="Email triage" trigger={kind:event,pattern:mail.new} steps=[
  { id:classify, name:Classify, action:{type:agent,prompt:"Classify as urgent or normal"}, outputKey:classification },
  { id:notify, name:Notify, dependsOn:[classify], condition:{path:payload.classification,op:eq,value:urgent}, action:{type:tool,pluginId:nusashell.notes,toolName:create,args:{title:URGENT}} }
]
```

Schedule-triggered (same schedule grammar as Jobs):

```
pipeline action=add name="Hourly digest" schedule="every 1h" steps=[
  { id:summary, name:Summary, action:{type:agent,prompt:"Summarize the last hour"} }
]
```

## Step fields

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | Unique step ID within the pipeline |
| `name` | yes | Display name |
| `action` | yes | `{ type: "agent", prompt: "..." }` or `{ type: "tool", pluginId, toolName, args }` |
| `dependsOn` | no | Array of step IDs that must complete first |
| `outputKey` | no | Store step output in context under this key |
| `condition` | no | Skip step if condition is false |

## Context passing

A step with `outputKey: "category"` stores its result in the pipeline context.
Downstream agent prompts can reference it via `{{context.category}}`:

```
Step A: outputKey="summary" → context.summary = "Meeting notes..."
Step B: prompt="Translate: {{context.summary}}" → resolved to "Translate: Meeting notes..."
```

### Template resolution by trigger mode

Agent prompts and tool args support templates. Which ones resolve depends on
how the run was triggered:

- `{{context.*}}` — **always resolves**, in both event-triggered *and*
  manual/schedule runs. Context is populated by earlier steps (`outputKey`).
- `{{payload.*}}` and `{{event.*}}` — resolve **only** when the run was triggered
  by a real automation event. In manual (`pipeline action=run`) or schedule
  runs there is no event, so these stay **literal** (`{{payload.x}}` is passed
  to the agent/tool unchanged — good for keeping literal braces in prompts).

```text
Manual / schedule run:
  prompt: "Handle {{payload.category}}"  → passed through literally
  prompt: "Use {{context.summary}}"      → resolved from earlier step output (if any)
Event-triggered run (mail.new, payload.category="finance"):
  prompt: "Handle {{payload.category}}"   → "Handle finance"
```

This avoids leaking a synthetic empty event (`type: ""`) into step templates on
manual/schedule runs.


## Conditional branching

Conditions evaluate against the event payload (`payload.*`) or accumulated
context (`context.*`):

- `{ path: "payload.urgent", op: "eq", value: "true" }` — skip if not urgent
- `{ path: "context.category", op: "ne", value: "spam" }` — skip if spam
- `{ or: [{ path: "...", op: "eq", value: "..." }, { path: "...", op: "contains", value: "..." }] }` — any match
- `{ not: { path: "...", op: "eq", value: "..." } }` — negate

Supported ops: `eq`, `ne`, `contains`, `regex`.

## Triggers

- **Event:** `trigger: { kind: "event", pattern: "mail.new", pluginId: "nusashell.mail" }`
  Optional: `throttleMs`, `maxFiresPerHour` (same semantics as jobs).
- **Schedule:** `schedule: "every 30m"` or `trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 30 } }`.
  Grammar matches Jobs: `every 30m` / `2h` / `1d`, 5-field cron, or ISO one-shot.
  Runs only while the app is open; one-shots past the grace window are marked missed.

Optional pipeline settings (honored at runtime):

- `settings.timeoutMs` — whole-run wall timeout; timed-out runs end with error
  code `PIPELINE_TIMEOUT`.

Max concurrency is **1** active run per pipeline. Concurrent fire attempts
(event or schedule) return `PIPELINE_ALREADY_RUNNING`.

Ignored / rejected fields (do not send): concurrent steps, `maxRetries`,
per-step timeouts.

## Managing pipelines

Call `pipeline` directly (no plugins plugin to enable). Actions: `list`,
`add`, `update`, `remove`, `run`, `cancel`, `runs`. Always `list` before creating a
duplicate. Plugin IDs / tool names / event patterns in examples are illustrative —
confirm real capabilities via `mcp_list` / `tool_list` before wiring them.

Lifecycle events (desktop UI + WS): `pipeline.started`, `pipeline.step_updated`,
`pipeline.completed`, `pipeline.failed`, `pipeline.cancelled`.

These `pipeline.*` events are **UI/telemetry/run-history events only** — they are
published as domain events, never as `AutomationEvent`s, so an event trigger
cannot match them (a pipeline must not trigger on its own lifecycle, which would
only enable a silent self-trigger loop). `validatePipelineTrigger` therefore
**rejects event patterns in the `pipeline.*` namespace** (e.g. `pipeline.completed`,
`pipeline.*`). For chaining follow-up work after a pipeline run, use an explicit
upstream automation event or a downstream scheduled/job `onComplete` emit that
targets a non-`pipeline.*` pattern.

## Run history and outputs

- `pipeline action=runs id=<pipelineId> limit=10` — recent runs (default compact:
  status/summary per step, no large previews)
- `pipeline action=runs id=<pipelineId> include_body=true` — include bounded
  step output previews
- `pipeline action=run_get run_id=<runId> include_body=true` — single run detail

Step summaries and previews are size-capped in the store so history cannot grow
unbounded. Downstream `outputKey` context is also capped for prompt safety.
