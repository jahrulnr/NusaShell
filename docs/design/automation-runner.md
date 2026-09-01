# NusaShell Automation Runner — Technical Design

Status: Implemented (local executor)
Target: NusaShell / `jahrulnr/NusaShell`

## 1. Summary

NusaShell Automation Runner is a local-first execution subsystem embedded in the Go process. A repository may contain an automation pipeline directory. Humans run workflows from the Automation UI; the agent uses the `automation` dispatcher. The first executor is a local process. Docker and remote runners are ports, not this release.

```text
Pipeline YAML → Parser/Validator → DAG → Scheduler → Executor
                                              │
                                    artifacts / logs / events
                                              │
                                    UI (WebSocket) + agent tools
```

## 2. Principles

- Agent-first API: every UI operation has an RPC method and a tool.
- Human-first observability: the default view is the run graph, not the YAML editor.
- Local-first execution with explicit host-trust.
- DAG over stages: `needs` is the scheduler input.
- Small core: no GitHub Actions expression language, marketplace actions, or Kubernetes.

## 3. Pipeline file

Canonical path: `.nusashell/pipeline.yaml`. Schema version 1. Jobs declare `needs`, sequential `steps` (`run`, `uses`, `wait_until`, `agent`), optional artifacts and cache. Triggers may be `manual`, `once`, `every`, or `when`. Cron and interval are distinct: cron is calendar-based, interval is elapsed time.

## 4. Runtime

- Domain types live in `domain/` (workflow, run, DAG, validation).
- Application ports and schedulers live in `application/` (`ExecutionScheduler`).
- YAML loader, SQLite store, and local executor live in `infrastructure/automation/`.
- RPC methods are `automation.*` in `contracts/automation.go`.
- Runs persist as snapshots. Waiting (`wait_until`) parks the run without occupying the executor. Disabled/missing MCP capabilities produce BLOCKED, not FAILED, when the provider exists but is unavailable.

## 5. Out of scope for this release

Docker executor, remote runner fleets, `for_each` fan-out, GitHub/GitLab YAML compatibility, hosted secrets, Kubernetes.
