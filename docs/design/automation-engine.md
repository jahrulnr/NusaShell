# NusaShell Automation Engine — Scheduling, Events, and Dynamic Workflows

Status: Implemented as the trigger layer above the Automation Runner
Target: NusaShell / `jahrulnr/NusaShell`

## 1. Split of responsibilities

The Automation Runner answers “how do we execute jobs after a trigger?” The Automation Engine answers “what starts a workflow, when, and under which conditions?”

```text
once / every / when / manual
        │
  AutomationScheduler  →  create Run  →  ExecutionScheduler  →  local executor
        │
  SQLite schedules, events, waits, locks
```

NusaShell owns durable timers. Agents must not keep their own sleep loops for scheduled work.

## 2. Trigger families

| Family | YAML | Semantics |
| --- | --- | --- |
| once | `triggers: [{once: {at}}]` | One-shot. Survives process restart. Does not re-fire. |
| every | `cron` or `interval` | Recurring. Cron uses calendar fields; interval uses elapsed duration. Not interchangeable. |
| when | `event` + optional `where` | Event bus. Delivery is idempotent per `(event_id, trigger_id, workflow_id)`. |
| manual | `manual: true` | UI / agent / RPC only. |

Concurrency policies: `allow`, `queue`, `replace`, `skip`. Debounce is per trigger.

## 3. Waiting

A `wait_until` step stores a wait record and sets run status `waiting`. The executor is released. `FireDue` resumes due waits after restart.

## 4. Persistence

All automation state is in `{dataDir}/automation/workflows.db` (SQLite). Definitions are not stored in conversation JSON.

## 5. Agent tools

`automation` (`op=run|wait|status|logs|cancel|steer|list|read|validate|create|enable|disable|delete`), `automation_schedule` (`op=once|every`), and `wait_until`. Creating a schedule through a dispatcher persists it; the process ticker (`FireDue` every 15s) is the only clock.
