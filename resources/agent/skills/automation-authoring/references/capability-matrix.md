# Automation capability matrix

This matrix is the truth boundary for authoring. `Supported` means the
current local runtime executes it. `Partial` means the YAML/model exists but
important production behavior is absent. `Integration-dependent` means the
workflow can be authored, but an event publisher or MCP provider must exist
and be running. `Blocked` means the current installation cannot run it now.

| Area | Status | Authoring rule | Evidence / caveat |
| --- | --- | --- | --- |
| `manual`, `once`, `every` triggers | Supported | Use the matching family and explicit time zone for calendar schedules | YAML loader + scheduler |
| `when` event trigger and `where` | Supported / integration-dependent | Match exact event type and publisher fields; generic MCP publishers use `notifications/nusashell/event` | Scheduler ingests pushed events; generic publishers are required |
| Event delivery deduplication | Supported | Keep event ID stable; still make remote effects idempotent | Key is event ID + trigger ID + workflow ID |
| Debounce | Supported | Use for bursts, not as a queue | Per workflow + trigger last-fire timestamp |
| Sequential steps | Supported | Keep a job's steps ordered and small | Executor runs steps in order |
| Independent DAG jobs | Supported | Use `needs` for fan-in and inspect failure propagation | Executor runs ready jobs in parallel |
| `if` conditions | Supported, small language | Use booleans, equality, boolean operators, dotted event/job/output paths, `subject_contains` only | No loops, arbitrary functions, or rich expressions |
| `run` shell | Supported locally | Treat commands as host-side effects; pin shell and timeout when needed | Local executor; `runs_on` is not remote capacity by itself |
| `uses` capability | Supported if resolved | Discover/validate exact capability and provider availability first | Only `uses` enters capability validation; agent MCP is runtime-discovered |
| `wait_until` | Supported | Use for long pauses and restart-safe resume | Parks executor and resumes after due time |
| `agent` step | Supported if agent/provider configured | Keep prompt bounded; use `${event.*}` only | Headless turn uses a new hidden automation conversation |
| `${event.*}` in agent prompt | Supported | Use event identity and quote untrusted data as data | Missing values render empty; no shell/output interpolation |
| `agent.output_schema` | Partial | Treat as documentation of desired shape; validate in prompt or a following script | Headless runner currently returns `{output: text}` and ignores schema |
| Job `retry` | Partial | Declare only for transient runner/timeout policy, but verify behavior | Parser/model exist; executor does not yet loop job attempts |
| Agent provider retry | Supported internally | Do not confuse it with job retry or a new workflow run | Interactive provider retry policy is internal to headless turn |
| `allow`/`skip`/`replace` concurrency | Supported with caveats | Pick by effect semantics and use a resource-scoped key | Replace cancels active run; skip drops new run |
| `queue` concurrency | Partial | Do not rely on it for durable FIFO/backlog | Current scheduler keeps the lock and skips the new run |
| Missed schedule policy | Supported at scheduler contract level | Choose deliberately for once/every workloads | Exact catch-up behavior depends on policy and schedule state |
| Webhook completion summary | Supported | Use bounded endpoint; never put secrets in URL | Delivery failures become events and do not block the run |
| Logs/status/wait/steer | Supported | One async run + one wait; logs/status for diagnosis | `steer` only while an agent step is running |
| Hidden pipeline conversations | Supported | Use run/step ID and `steer`, not Agent room list | Each headless agent step starts a new automation transcript |
| Job outputs in conditions | Supported | Use outputs for `if` only when the capability actually returns them | Shell executor does not capture stdout as structured outputs |
| Cross-job artifacts | Partial | Prefer explicit shared files and test the executor | Artifact ports/models exist; current store/executor wiring is incomplete |
| Cache | Partial | Never make correctness depend on it | Cache model/ports exist; list/clear/execute paths are incomplete |
| Remote runners / `runs_on` | Partial | Treat execution as local unless a runner is proven live | Runner model exists; default local executor is the reliable path |
| GitHub event ingestion | Integration-dependent / blocked in current audit | Use generic event template only after a publisher + MCP is installed | No GitHub MCP was live during audit |
| Kanban event/action integration | Integration-dependent / blocked in current audit | Enable Kanban plugin and discover live tools before use | `nusashell.kanban` was registered but stopped during audit |
| Telegram bridge | Supported when logged in/running | Verify exact chat/message IDs and observed tool result | `nusashell.telegram` was running during audit |
| First-class human approval/wait-for-signal | Not a YAML primitive | Keep approval in plugin/tool or split mutation into a manual workflow | Do not invent `approval`/`signal` step syntax |
| Durable search/resume by business key | Not a YAML primitive | Use run IDs, logs, and external runbook | Full Temporal-style query/signal semantics are not present |

When a request depends on a partial or blocked row, say so before activation.
A valid YAML parse is not the same as a runnable, safe integration.
