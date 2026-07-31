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
├── commands/                 # add-job, set-job-enabled, run-job-now, remove-job
├── queries/                  # list-jobs, job-output, validate-schedule
└── services/
    ├── job-agent-tool-gateway.ts   # restricted gateway (denies memory/skill tools)
    ├── job-agent-executor.ts       # headless AgentTurnRunner with inactivity watchdog
    └── job-scheduler.ts            # 60s tick, file lock, due selection, dispatch
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
`skill_list`, `skill_search`, `skill_read`. Jobs cannot mutate the user's
learning stores. MCP plugin tool discovery/granting and docs tools are
allowed.

### JobAgentExecutor

Builds a fresh `AgentTurnRunner` per run with:
- its own `traceId` (UUID),
- no streaming callbacks (headless),
- no memory injection,
- the restricted `JobAgentToolGateway`,
- an inactivity watchdog (default 600s) that aborts the turn.

Job turns are **never** persisted into `agent-conversations.json`.

### JobScheduler

- 60s tick interval (configurable via `NUSASHELL_JOBS_TICK_SECONDS`).
- Exclusive `.tick.lock` file in the jobs root (reaps stale locks by PID
  liveness and age).
- `listDue` → `claimFire` (at-most-once) → dispatch → `markRun` →
  `releaseFire`.
- Output persisted to `{jobsRoot}/output/{jobId}/{timestamp}.md` and
  metadata stored via `appendOutput`.
- Publishes `job.completed` / `job.failed` application events.

## WS protocol

| Method | Kind | Description |
| --- | --- | --- |
| `job.add` | command | Create a new job |
| `job.list` | query | List all jobs |
| `job.set-enabled` | command | Pause/resume a job |
| `job.run` | command | Fire a job immediately |
| `job.remove` | command | Delete a job |
| `job.output` | query | Recent output entries |
| `job.validate-schedule` | query | Validate a schedule expression |

Events: `job.completed`, `job.failed` (mapped to client event envelopes by
`client-event.mapper.ts`).

## Settings

- `NUSASHELL_JOBS_TICK_SECONDS` — scheduler tick interval (default 60).
- `NUSASHELL_JOBS_TIMEOUT_SECONDS` — agent inactivity timeout (default 600).
- `jobs.*` in the AI settings JSON blob (tick, timeout, enabled).

## Known gaps

- Jobs run only while the app is open. This is documented in the UI hint.
- No cross-device sync. Jobs are local to the machine.
- No job dependencies or chaining.
- Cron is UTC-only.
