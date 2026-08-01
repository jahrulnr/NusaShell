# Job Automation Waist

Scheduled durable jobs that fire headless agent turns or plugin tool calls
while NusaShell is open.

## Overview

The job automation waist adds a thin scheduling layer on top of the existing
agent runtime and plugin tool infrastructure. A **Job** is a durable,
time-triggered unit of work that runs either:

- **Agent mode** — a headless agent turn with a fixed prompt, or
- **Tool mode** — a direct plugin tool call with static args.

Jobs are **not** a replacement for cron or a distributed scheduler. They run
only while the NusaShell app is open. This is an intentional scope decision
documented honestly in the UI.

## Schedule grammar

The schedule parser (`packages/application/src/job/schedule-parser.ts`)
accepts:

| Input | Meaning |
| --- | --- |
| `every 30m` / `30m` | interval every 30 minutes |
| `every 2h` / `2h` | interval every 2 hours |
| `every 1d` / `1d` | interval every 1 day |
| `0 9 * * *` | 5-field cron (UTC) |
| `2025-12-01T09:00:00Z` | one-shot at a specific time |

### Grace rules

- **One-shot grace:** a `once` job whose `runAt` is in the past but within
  120 seconds of `now` is still fired. Beyond that, it is rejected at parse
  time.
- **Recurring catchup:** a recurring job late beyond `max(120s, min(period/2,
  2h))` fires once and fast-forwards `nextRunAt` past the missed slots
  (at-most-once, no catchup storm).
- **Missed one-shot:** a `once` job discovered at tick time with `runAt`
  older than the 120s grace (e.g. the app was closed) is marked `error` +
  `disabled` and a `job.failed` event is published. It is **not** silently
  fired.

## Cron matcher

A dependency-free 5-field cron matcher lives in `schedule-parser.ts`. It
supports `*`, ranges (`1-5`), lists (`1,3,5`), and step values (`*/15`,
`0-59/5`). Fields are minute, hour, day-of-month, month, day-of-week (0=Sun).
Search is bounded at 366 days to avoid infinite loops on impossible
expressions.

## Architecture

```
packages/application/src/job/
├── job-model.ts              # Job, JobSchedule, JobMode types + grace helpers
├── schedule-parser.ts        # parseSchedule + computeNextRun + cron matcher
├── ports/
│   └── job-store.port.ts     # JobStorePort interface
├── commands/                 # add-job, set-job-enabled, run-job-now, cancel-job, remove-job
├── queries/                  # list-jobs, job-output, validate-schedule
└── services/
    ├── job-agent-tool-gateway.ts   # restricted gateway (denies memory/skill tools)
    ├── job-agent-executor.ts       # headless AgentTurnRunner with inactivity watchdog
    └── job-scheduler.ts            # 60s tick, file lock, due selection, dispatch, cancel
```

### JobStore

- **SqliteJobStore** — uses the shared WAL `SqliteDatabase` and the
  `002-jobs.sql` migration. Schedule and mode are stored as JSON sidecars.
- **JsonJobStore** — fallback for dev environments without SQLite. Uses
  atomic staging-file + rename, mirroring the `SkillCuratorScheduler`
  pattern.

Both implement `JobStorePort` with fire-time dedup via `claimFire`/
`releaseFire` (a per-job claim with a TTL).

### JobAgentToolGateway

Wraps `McpAgentToolGateway` with a denylist: `memory`, `skill_manage`,
`skill_list`, `skill_search`, `skill_read`, `ask_question`, and `job`. Jobs
cannot mutate the user's learning stores, and the `job` meta-tool is denied
inside scheduled job turns to prevent recursion. MCP plugin tool
discovery/granting and docs tools are allowed.

## Agent tools

The foreground agent can manage jobs through the `job` meta-tool on
`McpAgentToolGateway` — full CRUD parity with the desktop Jobs surface and the
`job.*` WS methods. One tool with an `action` enum keeps the always-present
catalog small (same envelope style as `memory`):

| action | Required args | Behavior |
| --- | --- | --- |
| `list` | — | Compact jobs: `id`, `name`, `schedule`, `enabled`, `nextRunAt`, `lastStatus`, `running`, `activeTraceId` |
| `validate_schedule` | `schedule` | Parse + describe; returns `{ ok, data: { description } }` or a `job_invalid_schedule` envelope |
| `add` | `name`, `schedule`, `mode` (`agent`\|`tool`), plus mode fields / optional `repeat_times` | Same schedule parsing and `JOB_INVALID_SCHEDULE` mapping as `AddJobHandler` |
| `set_enabled` | `id`, `enabled` | Pause/resume; recomputes `nextRunAt` like `SetJobEnabledHandler` |
| `run` | `id` | `scheduler.runOneNow` (manual fire, respects the claim lock) |
| `cancel` | `id` | `scheduler.cancel` (abort in-flight run; `JOB_NOT_RUNNING` if not active) |
| `remove` | `id` | Delete |
| `output` | `id`, optional `limit` (1-100, default 20) | Recent output entries |

Returns `{ ok, data?, error?, meta }` like the other shell-owned meta-tools.
`ApplicationError` codes `JOB_NOT_FOUND`, `JOB_INVALID_SCHEDULE`, and
`JOB_NOT_RUNNING` are mapped into structured `error` envelopes (`job_not_found`,
`job_invalid_schedule`, `job_not_running`) so a bad action never crashes the turn.

### Wiring

The agent is constructed before jobs in the composition root, so job deps are
late-bound: `McpAgentToolGateway.bindJobs(store, scheduler)` is called in
`apps/backend/src/container.ts` after `createJobRuntime(...)`. The `job` tool
only appears in `listTools()` when both deps are bound. The `ReviewAgentToolGateway`
whitelist is unchanged — review stays learning-only (memory + skills), so `job`
is not available during background review turns either.

### JobAgentExecutor

Builds a fresh `AgentTurnRunner` per run with:
- its own `traceId` (UUID),
- no streaming callbacks (headless),
- no memory injection,
- the restricted `JobAgentToolGateway`,
- an inactivity watchdog (default 600s) that aborts the turn,
- an external `AbortSignal` from the scheduler (bridged with the watchdog
  controller so either inactivity-timeout or user-cancel aborts the turn).

Job turns are **never** persisted into `agent-conversations.json`.

### JobScheduler

- 60s tick interval (configurable via `NUSASHELL_JOBS_TICK_SECONDS`).
- Exclusive `.tick.lock` file in the jobs root (reaps stale locks by PID
  liveness and age).
- Tick pipeline:

```mermaid
flowchart LR
  ListDue["listDue"] --> ClaimFire["claimFire at-most-once"]
  ClaimFire --> Dispatch["dispatch"]
  Dispatch --> MarkRun["markRun"]
  MarkRun --> ReleaseFire["releaseFire"]
```
- Output persisted to `{jobsRoot}/output/{jobId}/{timestamp}.md` and
  metadata stored via `appendOutput`. The `traceId` is persisted on each
  `JobOutputEntry` for run correlation.
- Active-run tracking: a `Map<jobId, { traceId, controller, startedAt }>`
  entries are registered while `dispatch` is in flight. `isRunning(jobId)`
  and `activeTraceId(jobId)` expose live state to the UI and agent tool.
- `cancel(jobId)` aborts the in-flight `AbortController`, publishes
  `job.cancelled`, and marks the run as `lastStatus: "cancelled"`.
- Publishes `job.started`, `job.completed`, `job.failed`, and
  `job.cancelled` application events.

## WS protocol

| Method | Kind | Description |
| --- | --- | --- |
| `job.add` | command | Create a new job |
| `job.list` | query | List all jobs (returns `{ jobs: [...] }`) |
| `job.set-enabled` | command | Pause/resume a job |
| `job.run` | command | Fire a job immediately |
| `job.cancel` | command | Cancel an in-flight job run |
| `job.remove` | command | Delete a job |
| `job.output` | query | Recent output entries (returns `{ outputs: [...] }`; pass `includeBody: true` to include full markdown) |
| `job.validate-schedule` | query | Validate a schedule expression |

Events: `job.started`, `job.completed`, `job.failed`, `job.cancelled` (mapped
to client event envelopes by `client-event.mapper.ts`). Each event includes
a `traceId` for run correlation.

## Settings

- `NUSASHELL_JOBS_TICK_SECONDS` — scheduler tick interval (default 60).
- `NUSASHELL_JOBS_TIMEOUT_SECONDS` — agent inactivity timeout (default 600).
- `jobs.*` in the AI settings JSON blob (tick, timeout, enabled).

## Known gaps

- Jobs run only while the app is open. 
- No cross-device sync. Jobs are local to the machine.
- No job dependencies or chaining (e.g. planning auto-triggers execute).
- Cron is UTC-only.
- No live token streaming into the Jobs UI; full output is available
  post-run via the output modal's "Show full output" button.
