# NusaShell CI Runner — Technical Design

Status: Proposed
Target: NusaShell Light / `jahrulnr/NusaShell-agent`
Primary implementation language: Go
Frontend: embedded native ES modules (vanilla JS), no frontend build step

## 1. Executive summary

NusaShell should not clone GitHub Actions or GitLab CI verbatim. It should clone the useful execution model: a declarative pipeline, jobs as isolated execution units, dependency-aware scheduling, runner/executor abstraction, artifacts, caches, logs, cancellation, retries, and a human-readable run graph. The NusaShell-specific value is that the same system is directly usable by the AI agent.

The recommended product is **NusaShell Tasks/Runner**: a local-first execution subsystem embedded in the existing Go process. A repository can contain `.nusashell/pipeline.yaml` describing jobs. A human can run a pipeline from the UI, inspect the DAG, logs, artifacts and runner state, or execute one job. The agent can perform the same operations through structured built-in tools instead of inventing shell commands or scraping UI state.

The first implementation should use a local process executor and an optional Docker executor. Do not start with Kubernetes, remote runner fleets, distributed scheduling, or a GitHub/GitLab compatibility layer. The architecture should leave explicit ports for those capabilities later.

The core abstraction is:

```text
Pipeline definition
       |
       v
  Parser/Validator
       |
       v
   Pipeline DAG
       |
       v
   Scheduler -----> Artifact/Cache Store
       |
       v
     Runner
       |
       +---- local executor
       +---- docker executor (phase 2)
       +---- remote executor (future)
       |
       v
 Job events/logs/status
       |
       +---- WebSocket -> human UI
       +---- application state -> agent tools
```

## 2. Source basis and current NusaShell architecture

The current architecture is already a good fit for this feature. NusaShell is a Go binary with an embedded vanilla JS/HTML/CSS frontend. Its dependency rule is inward: domain is pure, application owns use cases and ports, transport owns HTTP/WebSocket/SSE, and infrastructure implements ports. The existing transports expose `/rpc`, `/events`, and `/ws`; browser event delivery is already designed around WebSocket while `/rpc` remains request/response. The application already has an in-memory event bus and long-running agent turn lifecycle. fileciteturn0file0L1-L35

The current built-in toolbox is also the right extension point. `infrastructure/tools/toolbox.go` already exposes structured tools for skills, memory, todos, documentation, MCP management, image handling, and web operations, and dynamically exposes enabled MCP tools. A CI subsystem should follow the same pattern rather than adding an unrelated command interface. fileciteturn9file0L2-L2

The repository already has a meaningful verification baseline: frontend tests, Go formatting, UI documentation generation, `go vet`, race-enabled tests, and cross-platform builds. The current GitHub Actions workflow runs frontend tests, backend tests on Ubuntu/Windows/macOS, and builds on all three platforms. fileciteturn3file0L2-L2 The Makefile exposes the same verification concepts locally through `check`, `test`, `race`, `vet`, `build`, frontend checks, and generated-document/catalog checks. fileciteturn4file0L2-L2

The existing architecture also already defines a workspace per conversation and allows the frontend to select a real local workspace path. That workspace should become the default working directory for agent-invoked jobs when the user explicitly authorizes execution there. fileciteturn0file0L36-L56

## 3. What GitHub Actions and GitLab Runner teach us

GitHub Actions and GitLab CI solve the same fundamental problem with different terminology. GitHub centers on workflows, jobs, steps, runners, actions, artifacts and caches. GitLab centers on pipelines, jobs, stages/needs, runners/executors, artifacts and caches.

GitLab Runner has a particularly useful architectural separation: the runner manager receives work and executes it through an executor. GitLab currently supports executors such as Docker, Kubernetes and Shell. The Docker executor creates an isolated container per job, while the Kubernetes executor creates a pod for each job. citeturn0search8turn0search1turn0search0

GitLab's Shell executor demonstrates an important trade-off for NusaShell. It is simple and runs directly on the host, but has limited isolation and is unsafe for untrusted jobs. Therefore NusaShell should offer a local executor for trusted, personal use, but make the isolation boundary explicit in the UI and data model. citeturn0search3

GitLab's `needs` model is worth copying conceptually. It turns a pipeline into a DAG instead of forcing every job into sequential stages. Independent jobs can start as soon as their dependencies finish. citeturn1search3 GitHub Actions similarly models dependencies with job-level `needs` and supports reusable workflow abstractions; GitHub distinguishes reusable workflows, which contain multiple jobs, from composite actions, which bundle steps inside one job. citeturn0search6turn0search2

Artifacts and caches should remain separate concepts. GitLab explicitly recommends cache for reusable dependencies and artifacts for intermediate build output passed between jobs. citeturn1search1 GitHub likewise treats dependency caching as an optimization across runs rather than the authoritative transport for build outputs. citeturn1search8

GitHub's runner labels/groups also suggest a clean future model for NusaShell. Jobs should declare requirements such as OS, architecture, executor and capabilities; runners advertise labels/capabilities. GitHub routes jobs only to an online, idle runner matching those requirements. citeturn0search7turn0search10turn0search9

GitLab's autoscaling model reinforces another principle: scheduling and machine provisioning should be separate. A runner manager can decide that capacity is needed while a separate autoscaler creates machines. citeturn0search4turn0search5 NusaShell should therefore define a `RunnerProvider`/`Executor` port now, even though the first implementation is local.

## 4. NusaShell product principles

NusaShell's CI system should follow six principles.

First, **agent-first API**. Every operation available in the UI must have a stable application use case and structured result. The agent must never need to click the UI, parse terminal output, or infer job IDs from logs.

Second, **human-first observability**. The UI must make a pipeline understandable without reading YAML. The default view is the run graph and status, not a configuration editor.

Third, **local-first execution**. The default runner is the current machine. This matches NusaShell's existing personal/local architecture.

Fourth, **explicit trust**. Local execution can run arbitrary commands. The UI must clearly show that the job has host access. A future isolated executor can provide stronger boundaries.

Fifth, **DAG over stages**. Stages can be supported as a convenience grouping, but `needs` defines the actual dependency graph.

Sixth, **small stable core**. Avoid implementing the enormous surface area of GitHub Actions expressions, GitLab YAML inheritance, marketplace actions, remote secrets management, hosted runner fleets, or Kubernetes orchestration in the first release.

## 5. Proposed terminology

Use NusaShell-native terminology internally while keeping familiar terminology in the UI.

| NusaShell | GitHub Actions | GitLab |
|---|---|---|
| Pipeline | Workflow | Pipeline |
| Job | Job | Job |
| Step | Step | Script/section |
| Run | Workflow run | Pipeline |
| Runner | Runner | Runner |
| Executor | Runner execution | Executor |
| Needs | `needs` | `needs` |
| Artifact | Artifact | Artifact |
| Cache | Cache | Cache |
| Capability | Runner label | Runner tag |
| Pipeline file | workflow YAML | `.gitlab-ci.yml` |

Recommended user-facing term: **Pipeline**.

Recommended local file: `.nusashell/pipeline.yaml`.

## 6. Pipeline file format

Do not attempt full GitHub Actions or GitLab CI syntax compatibility. NusaShell needs a small canonical schema that is easy for both humans and agents to generate correctly.

Example:

```yaml
version: 1
name: NusaShell verification

triggers:
  manual: true

defaults:
  shell: auto
  timeout: 10m

env:
  CI: "true"

jobs:
  test-backend:
    name: Backend tests
    runs_on: [local, linux, amd64]
    steps:
      - name: Format
        run: gofmt -l .
      - name: Vet
        run: go vet ./...
      - name: Test
        run: go test -race ./...

  test-frontend:
    name: Frontend tests
    runs_on: [local]
    steps:
      - name: Install dependencies
        run: npm ci
      - name: Test
        run: node --test frontend/*.test.mjs

  build:
    name: Build
    needs: [test-backend, test-frontend]
    steps:
      - name: Build
        run: go build ./...
      - name: Package
        run: mkdir -p dist && cp ./bin/nusashell ./dist/
    artifacts:
      paths:
        - dist/
      retention: 7d
```

The canonical schema should contain only these initial concepts:

`version`, `name`, `triggers`, `env`, `defaults`, `jobs`, `job.name`, `job.needs`, `job.runs_on`, `job.env`, `job.timeout`, `job.continue_on_error`, `job.steps`, `step.name`, `step.run`, `step.shell`, `step.env`, `step.timeout`, `artifacts`, `cache`, and `if`.

The `if` expression should initially be intentionally small: boolean literals, event name, branch, tag, and previous job status. Do not implement a general-purpose expression language until actual workflows require it.

## 7. Execution model

A PipelineRun is an immutable snapshot of the validated pipeline plus runtime metadata. Never execute directly from a mutable file after the run has started.

Suggested domain entities:

```text
PipelineDefinition
PipelineJob
PipelineStep
PipelineRun
JobRun
StepRun
Runner
Artifact
CacheEntry
ExecutionLog
```

Suggested status enum:

```text
queued
waiting
running
success
failed
cancelled
skipped
blocked
expired
```

A pipeline run proceeds as follows:

```text
create run
  -> resolve repository/workspace
  -> parse pipeline
  -> validate schema
  -> build DAG
  -> persist run snapshot
  -> enqueue ready jobs
  -> scheduler selects runnable job
  -> runner claims job
  -> executor prepares workspace
  -> execute steps sequentially
  -> collect logs/artifacts/cache
  -> publish completion
  -> unlock dependent jobs
  -> repeat
  -> finalize pipeline
```

Within a job, steps execute sequentially. Between jobs, execution is parallel wherever the DAG permits it.

The scheduler must never infer dependencies from stage names. `needs` is authoritative.

## 8. Runner and executor architecture

Separate runner identity from executor implementation.

```go
type Runner struct {
    ID           string
    Name         string
    Labels       []string
    Executor     string
    Status       RunnerStatus
    MaxParallel  int
    Capabilities []Capability
}
```

The application should expose a port similar to:

```go
type JobExecutor interface {
    Prepare(ctx context.Context, req PrepareRequest) (ExecutionWorkspace, error)
    RunStep(ctx context.Context, req RunStepRequest) (StepResult, error)
    Cleanup(ctx context.Context, req CleanupRequest) error
}
```

The first implementations are:

`local`: executes using the host shell in the selected workspace.

`docker`: creates a temporary container with the workspace mounted, explicit environment, resource limits and network policy. This is the recommended executor for less-trusted jobs.

`remote`: reserved interface only. It can later map to a remote NusaShell runner without changing the scheduler or UI.

Do not make the scheduler know anything about Docker, shell, Windows, macOS, or Linux. It should only ask an executor to run a job.

## 9. Local executor details

The local executor should use Go's `os/exec` and create one process per step. It must capture stdout and stderr separately at the process boundary and merge them into a timestamped event stream for display.

The process environment is constructed from:

```text
base runner environment
+ pipeline env
+ job env
+ step env
+ NusaShell CI variables
```

Recommended built-in CI variables:

```text
NUSASHELL=true
NUSASHELL_CI=true
NUSASHELL_PIPELINE_ID
NUSASHELL_RUN_ID
NUSASHELL_JOB_ID
NUSASHELL_STEP_ID
NUSASHELL_WORKSPACE
NUSASHELL_OS
NUSASHELL_ARCH
```

Do not expose credentials as ordinary environment variables unless explicitly requested by the job. Secret values must be masked in logs.

The executor must support cancellation through `context.Context`, terminate the process group, and enforce a hard timeout. The application must persist a cancellation request so a UI reconnect cannot lose the state transition.

## 10. Workspace model

A job needs an explicit workspace. For local runs, default to the current conversation workspace when a pipeline is invoked by the agent, or the selected repository/workspace when invoked from the UI.

Each job should normally receive a clean job directory derived from the run workspace:

```text
<data>/ci/runs/<run-id>/jobs/<job-id>/workspace/
<data>/ci/runs/<run-id>/jobs/<job-id>/logs/
<data>/ci/runs/<run-id>/jobs/<job-id>/artifacts/
```

For phase 1, source checkout can simply operate on the selected existing workspace. Add clean checkout support later when NusaShell manages remote repositories directly.

Never let artifact extraction overwrite files outside the job workspace.

## 11. Artifacts

Artifacts are durable outputs of a job and are part of the run record. They are not dependency caches.

Recommended storage:

```text
<data>/ci/artifacts/<run-id>/<job-id>/<artifact-id>.tar.zst
```

Metadata should contain name, paths, size, checksum, created time, expiration, producing job, and content type.

A downstream job should consume artifacts explicitly:

```yaml
build:
  artifacts:
    paths: [dist/]

test-package:
  needs:
    - job: build
      artifacts: true
```

The first implementation may copy/extract artifacts locally. The storage interface must remain abstract so object storage can be added later.

## 12. Cache

Cache is an optimization and must never be required for correctness.

The cache key should be deterministic and normally incorporate a user-selected namespace plus dependency fingerprints. Examples include `go.sum`, `package-lock.json`, toolchain version, OS and architecture.

```text
cache key = namespace + hash(lockfiles/toolchain/platform)
```

Use a content-addressed or hashed archive format where practical. Avoid treating the working directory itself as a cache.

Cache operations should have explicit hit/miss events. The UI should say `Cache hit`, `Cache miss`, or `Cache unavailable` rather than hiding cache behavior.

This follows the GitLab distinction between reusable dependency cache and job artifacts. citeturn1search1turn1search0

## 13. Logs and events

Do not store only one giant log string. Model logs as append-only chunks.

```go
type LogChunk struct {
    RunID       string
    JobID       string
    StepID      string
    Sequence    uint64
    Timestamp   time.Time
    Stream      string // stdout, stderr, system
    Text        string
}
```

Publish structured events through the existing `application.Bus`.

Suggested event vocabulary:

```text
ci.run.created
ci.run.started
ci.run.completed
ci.run.failed
ci.run.cancelled
ci.job.queued
ci.job.started
ci.job.completed
ci.job.failed
ci.job.cancelled
ci.job.skipped
ci.step.started
ci.step.output
ci.step.completed
ci.step.failed
ci.artifact.created
ci.cache.hit
ci.cache.miss
ci.runner.online
ci.runner.offline
```

The WebSocket transport should deliver these events using the same event-envelope mechanism already used by the agent UI. The existing architecture explicitly centralizes event publication in the application bus and uses WebSocket for browser event delivery. fileciteturn0file0L17-L35

SSE remains available for non-browser clients; do not create a CI-specific streaming transport.

## 14. Application ports

Add a dedicated CI package under `application` rather than placing scheduler logic in `infrastructure`.

Suggested interfaces:

```go
type PipelineStore interface {
    GetDefinition(ctx context.Context, workspace string) (*domain.PipelineDefinition, error)
}

type PipelineRunStore interface {
    Create(ctx context.Context, run *domain.PipelineRun) error
    Get(ctx context.Context, id string) (*domain.PipelineRun, error)
    List(ctx context.Context, filter RunFilter) ([]domain.PipelineRun, error)
    Update(ctx context.Context, run *domain.PipelineRun) error
}

type JobRunStore interface {
    Get(ctx context.Context, id string) (*domain.JobRun, error)
    ListByRun(ctx context.Context, runID string) ([]domain.JobRun, error)
    Update(ctx context.Context, job *domain.JobRun) error
}

type ExecutionLogStore interface {
    Append(ctx context.Context, chunk domain.LogChunk) error
    Read(ctx context.Context, jobID string, after uint64) ([]domain.LogChunk, error)
}

type ArtifactStore interface {
    Put(ctx context.Context, req ArtifactPutRequest) (domain.Artifact, error)
    List(ctx context.Context, runID string) ([]domain.Artifact, error)
    Open(ctx context.Context, artifactID string) (io.ReadCloser, error)
}

type CacheStore interface {
    Get(ctx context.Context, key string) (CacheReader, error)
    Put(ctx context.Context, key string, src io.Reader) error
    Delete(ctx context.Context, key string) error
}

type RunnerRegistry interface {
    List(ctx context.Context) ([]domain.Runner, error)
    Claim(ctx context.Context, req ClaimRequest) (domain.Runner, error)
    Release(ctx context.Context, runnerID string) error
}

type JobExecutor interface {
    Prepare(ctx context.Context, req PrepareRequest) (ExecutionWorkspace, error)
    RunStep(ctx context.Context, req RunStepRequest) (StepResult, error)
    Cleanup(ctx context.Context, req CleanupRequest) error
}
```

## 15. Scheduler

The scheduler is the most important new application component.

Responsibilities:

1. Validate a run snapshot.
2. Compute the DAG.
3. Find jobs whose dependencies are satisfied.
4. Match jobs to available runners.
5. Claim runner capacity atomically.
6. Start execution.
7. Persist state transitions.
8. Release runner capacity.
9. Re-evaluate dependent jobs.
10. Stop or cancel the pipeline when requested.

The scheduler should be event-driven internally but must have a periodic recovery loop. A process crash must not leave jobs permanently in `running`.

Every running job should have a heartbeat/lease:

```text
job lease duration: 30s
heartbeat: 10s
recovery after: 60s without heartbeat
```

On startup, recover stale runs by marking orphaned jobs as `failed` or `cancelled` according to the last persisted intent, then allow an explicit retry.

Do not automatically rerun an arbitrary job after a process crash in phase 1 because the command may be non-idempotent.

## 16. Retry policy

Separate infrastructure retries from user-command retries.

Safe automatic retries:

- runner acquisition race
- transient internal storage error
- WebSocket delivery failure
- cache download failure, followed by cache miss

Do not automatically retry a failed shell command by default. A user can configure:

```yaml
retry:
  max_attempts: 2
  on: [runner_error, timeout]
```

Command failure should remain deterministic unless the pipeline explicitly asks for retry.

## 17. Built-in agent tools

The agent should get a small, high-signal CI toolbox. Do not expose every backend endpoint as a separate tool.

Recommended built-ins:

`ci_pipeline_list` — list pipeline definitions in the current workspace.

`ci_pipeline_read` — read and validate the current pipeline definition.

`ci_pipeline_validate` — validate YAML and report actionable errors with job/step paths.

`ci_run` — start a pipeline or selected jobs.

`ci_run_status` — return run status, DAG summary, active jobs and failures.

`ci_job_status` — inspect one job including current step and exit code.

`ci_logs` — retrieve logs with job/step filters and pagination.

`ci_cancel` — cancel a run or job.

`ci_retry` — retry a failed/cancelled job or run explicitly.

`ci_artifacts_list` — list artifacts produced by a run.

`ci_artifact_read` — read a text artifact or metadata; binary artifacts should return metadata and a local path rather than dumping bytes into the model context.

`ci_runner_list` — inspect runner availability and capabilities.

`ci_cache_clear` — clear a cache namespace when debugging stale dependencies.

The agent should normally use `ci_run_status` after `ci_run`, then `ci_logs` only for failing or relevant jobs. This avoids flooding the context with successful build output.

Tool results should be compact and structured. Example:

```json
{
  "run_id": "run_01J...",
  "status": "failed",
  "summary": {
    "success": 2,
    "failed": 1,
    "running": 0,
    "queued": 0
  },
  "failed_jobs": [
    {
      "id": "test-backend",
      "step": "Test",
      "exit_code": 1,
      "error": "go test -race ./... failed"
    }
  ]
}
```

This follows the existing NusaShell philosophy of exposing structured tools with explicit schemas through `Toolbox.ListTools()` and `Toolbox.Execute()`. fileciteturn9file0L2-L2

## 18. Agent behavior contract

The agent should not blindly execute a pipeline when the user asks for code changes. It should first inspect whether a pipeline exists.

Recommended behavior:

```text
User: run the tests
  -> ci_pipeline_list
  -> ci_pipeline_read
  -> ci_run
  -> ci_run_status
  -> if failed: ci_logs failed job
  -> diagnose/fix
  -> ci_retry or ci_run
```

For a request such as "build this project", the agent may infer a direct command only if no pipeline exists. Once `.nusashell/pipeline.yaml` exists, the pipeline becomes the canonical project verification entry point.

The agent should never fabricate a job name. If a requested job does not exist, `ci_run` returns the valid job IDs.

## 19. Human UI information architecture

Add a top-level **Pipelines** view to NusaShell. It should not look like a generic DevOps dashboard. The application is local and personal, so the UI should optimize for fast diagnosis rather than organizational reporting.

Primary navigation:

```text
Home
Chat
Pipelines
  - Runs
  - Pipeline definition
  - Runners
  - Caches
  - Artifacts
Plugins
Memory
Settings
```

The default Pipelines screen is the latest run, not a configuration form.

## 20. Pipeline Runs screen

The Runs screen should show:

```text
Pipelines                         [Run ▼] [Validate]

NusaShell verification
master • 2 minutes ago

● Success   4 jobs   1m 32s

┌──────────────────────────────────────────────┐
│ test-backend ────────┐                       │
│       ✓ 31s           ├──► build ──► package │
│ test-frontend ───────┘      ✓ 18s      ✓     │
└──────────────────────────────────────────────┘

Recent runs
✓ #42  2m ago   1m 32s
✕ #41  14m ago  43s
✓ #40  1h ago   1m 29s
```

The graph should be the primary visual. Each node shows status, duration, and a short failure marker. Clicking a node opens its job detail without leaving the run context.

Avoid large colored cards. Use status icons, subtle borders, typography, and a compact graph.

## 21. Job detail screen

Job detail should have three regions:

```text
[Back to run]  Backend tests                 Failed
              31s • runner: local-linux

[Summary] [Logs] [Artifacts] [Environment]

Steps
✓ Format                         0.3s
✓ Vet                            2.1s
✕ Test                           27.8s

-----------------------------------------------
27.8s  Test
$ go test -race ./...
...
FAIL: application/... 
```

The Logs tab should support live streaming, follow-tail, search, copy, and download. The default view should open near the failure rather than at line 1 for a failed job.

## 22. Pipeline definition UI

The definition view should be a split interface:

```text
┌──────────────────────┬────────────────────────────┐
│ pipeline.yaml        │ Visual preview              │
│                      │                             │
│ jobs:                │ test ──┐                   │
│   test:              │        ├──► build           │
│     steps:           │ lint ───┘                   │
│       - run: ...     │                             │
└──────────────────────┴────────────────────────────┘
```

Use a plain text editor with syntax highlighting only if it can be implemented without introducing a large frontend dependency. The current project deliberately uses native ES modules and no frontend build step, so avoid bringing in a heavy editor framework solely for this screen. The frontend is embedded directly into the Go binary. fileciteturn0file0L1-L15

Add generated forms only for common operations such as adding a job, selecting dependencies, setting timeout, and adding artifacts. The YAML remains the source of truth.

## 23. Runner UI

Runner screen:

```text
Runners

LOCAL MACHINE                         Online
linux • amd64 • local                 1 / 2 jobs

Capabilities
shell  git  go  node  docker

Execution
✓ 2 completed
● 1 running
○ 1 queued

[Pause runner]
```

Future remote runners should appear in the same list. The UI must distinguish `local`, `docker`, and remote executors clearly.

## 24. Artifacts UI

Artifacts should be attached to runs and jobs, not treated as a generic file browser.

Show:

```text
build / dist
3 files • 12.4 MB • expires in 6 days

[Download] [Open folder]
```

For text artifacts, provide a preview. For binary files, provide metadata and a download/open action.

## 25. Cache UI

The cache screen is primarily a debugging surface.

Show namespace, key, size, last hit, last write, and platform. Provide `Clear` only with a confirmation dialog.

Do not expose cache internals by default to the agent unless requested; stale cache debugging is a specialized operation.

## 26. Human/agent convergence

The most important UX decision is that the human and agent should see the same run objects.

If the agent executes `ci_run`, the user should immediately see the same run in Pipelines. If the human clicks Run, the agent can inspect it with `ci_run_status` in the same conversation.

This creates one execution model:

```text
                 PipelineRun
                /           \
           Human UI       Agent tools
               |               |
             WebSocket        RPC/tool
               |               |
               +------ Application ------+
                                      |
                                  Scheduler
                                      |
                                   Runner
```

Do not create a separate "agent execution" mode. The only difference is who requested the operation.

## 27. RPC contracts

Add methods under a `ci.*` namespace.

Recommended initial roster:

```text
ci.pipelines.list
ci.pipelines.read
ci.pipelines.validate
ci.runs.start
ci.runs.list
ci.runs.get
ci.runs.cancel
ci.runs.retry
ci.jobs.get
ci.jobs.logs
ci.jobs.cancel
ci.artifacts.list
ci.artifacts.get
ci.runners.list
ci.cache.list
ci.cache.clear
```

The response envelope must use the existing `{ok, result|error}` convention. Do not invent a second API envelope.

WebSocket events use the `ci.*` event names described above and the same event envelope as the existing agent transport.

## 28. Persistence

Do not put CI runs into the existing conversation JSON files. CI is an independent subsystem and will grow faster than conversations.

Recommended initial SQLite schema because the project already uses SQLite for credentials and CI queries naturally require filtering, ordering and relationships.

Tables:

```text
ci_pipelines
ci_pipeline_runs
ci_job_runs
ci_step_runs
ci_log_chunks
ci_artifacts
ci_cache_entries
ci_runners
```

Suggested indexes:

```text
ci_pipeline_runs(workspace, created_at DESC)
ci_job_runs(run_id, status)
ci_job_runs(run_id, job_id)
ci_step_runs(job_run_id, sequence)
ci_log_chunks(job_id, sequence)
ci_artifacts(run_id, job_id)
ci_cache_entries(namespace, key)
```

The exact schema should be finalized during implementation after the domain structs are stable.

## 29. Proposed repository layout

Extend the existing architecture without breaking its dependency direction:

```text
domain/
  ci_pipeline.go
  ci_run.go
  ci_job.go
  ci_runner.go
  ci_artifact.go
  ci_cache.go

application/
  ci_service.go
  ci_scheduler.go
  ci_ports.go
  ci_events.go

contracts/
  ci.go
  ci_methods.go
  ci_fixtures/

infrastructure/
  ci/
    yaml_loader.go
    sqlite_store.go
    log_store.go
    artifact_store.go
    cache_store.go
    local_executor.go
    docker_executor.go
    runner_registry.go

transport/
  ci_handlers.go

frontend/js/
  ci.js
  ci-runs.js
  ci-job.js
  ci-pipeline.js
  ci-runners.js
  ci-artifacts.js

resources/agent/docs/
  ui-ci-runs.md
  ui-ci-job.md
  ui-ci-pipeline.md
```

Keep the executor implementation in infrastructure. The scheduler and run state machine remain application-level.

## 30. Security and trust model

NusaShell currently has no authentication or rate limiting by design and listens on `127.0.0.1` by default. This is acceptable for a personal CI runner, but binding NusaShell to a trusted network must be treated as granting command execution capability. fileciteturn0file0L1-L4

For local execution, the UI must display a persistent trust indicator:

`Local runner — commands execute on this machine.`

For Docker execution, display:

`Docker runner — isolated container.`

Do not claim Docker provides perfect isolation. Resource limits, mounts, network access and privileged mode must be explicit.

GitLab documents the risks of exposing Docker's host socket and privileged Docker-in-Docker configurations; NusaShell should therefore never mount `/var/run/docker.sock` implicitly. citeturn0search0turn0search1

Secrets should be stored in the existing credential subsystem, never in pipeline YAML. Logs must pass through a masking layer before persistence and WebSocket delivery.

## 31. Secret model

Introduce a reference syntax rather than embedding secret values:

```yaml
env:
  NPM_TOKEN:
    secret: npm_token
```

The pipeline definition contains only the credential key. The executor receives the resolved value at runtime.

Secret values must be:

- excluded from persisted run metadata
- masked in stdout/stderr
- excluded from agent tool results
- excluded from error strings
- excluded from debug logging

The credential store should remain the only durable source of API/secret values, consistent with the existing architecture where credentials are kept in SQLite and not in JSON/JSONL state. fileciteturn0file0L80-L96

## 32. Resource limits

Local executor phase 1 should support timeout and concurrency. CPU/memory/process limits are platform-dependent and should not be faked.

Docker executor phase 2 should support:

```text
CPU limit
memory limit
pids limit
network mode
read-only root filesystem
workspace mount
working directory
```

Expose only settings that can be enforced reliably on the selected platform.

## 33. Cross-platform requirements

The current project explicitly builds and tests on Linux, Windows and macOS. fileciteturn3file0L2-L2 Therefore the local executor must not assume Bash.

Shell selection:

```text
Unix -> user's configured shell, default sh
Windows -> PowerShell Core if available, otherwise PowerShell
explicit shell -> validate availability before execution
```

The pipeline should support `shell: auto`, `sh`, `bash`, `pwsh`, and `powershell` initially.

Do not implement shell translation. A command is interpreted by the selected shell.

## 34. Pipeline validation

Validation must happen before a run is created.

Errors should be structured:

```json
{
  "path": "jobs.build.needs[0]",
  "code": "unknown_job",
  "message": "Job 'compile' does not exist"
}
```

Validation stages:

1. YAML syntax.
2. Schema validation.
3. Job ID uniqueness.
4. Dependency existence.
5. Cycle detection.
6. Invalid runner requirements.
7. Invalid artifact paths.
8. Invalid timeout/retry values.
9. Unsupported shell/executor options.

GitLab's CI Lint is a good precedent for separating syntax/logic validation and pipeline simulation. NusaShell should expose equivalent local validation through `ci_pipeline_validate`. citeturn1search9

## 35. Pipeline graph algorithm

Build an adjacency map:

```text
job -> dependencies
```

Validate acyclicity using DFS or Kahn's algorithm. At runtime, maintain:

```text
remaining_dependencies[job]
```

When a job succeeds, decrement dependents. When it fails, dependent jobs become `blocked` unless their dependency is marked optional or `continue_on_error` applies.

Do not recompute the entire graph after every event; maintain the run's in-memory scheduler state and persist transitions.

## 36. Concurrency

Initial configuration:

```text
max_pipeline_jobs: 4
max_total_jobs: 4
```

Allow a user setting later. A runner's `MaxParallel` should be authoritative for that runner.

The scheduler must avoid deadlocks when a pipeline requires more concurrency than available. Jobs simply remain queued.

For phase 1, use a single scheduler goroutine plus worker goroutines. This is simpler and aligns with the local-first architecture.

## 37. Failure UX

Failure should be actionable rather than merely red.

A failed job summary should contain:

```text
Test failed
exit code: 1
failed step: Test
duration: 27.8s
runner: local

Likely failure:
application/foo_test.go:42

[Open logs] [Retry job] [Ask agent to diagnose]
```

`Ask agent to diagnose` should create a normal agent action using the same `ci_job_status` and `ci_logs` tools. It should not create a special AI integration path.

## 38. Agent context efficiency

CI output can become a major context consumer. Never return full logs by default.

`ci_run_status` returns summaries only.

`ci_logs` defaults to:

```text
job: required
stream: stdout+stderr
tail: 200 lines
```

Support `before`, `after`, `grep`, and pagination. For a failed job, the agent should receive the failure region first.

Successful logs should be available but not automatically injected into the conversation.

Artifacts follow the same rule: metadata first, content on demand.

## 39. Built-in commands for humans

The UI should expose simple command actions without requiring YAML editing:

`Run pipeline`

`Run job`

`Retry failed`

`Cancel`

`Validate`

`Open logs`

`Download artifacts`

`Clear cache`

The UI should not expose a giant settings matrix. Advanced YAML features remain available in the pipeline file.

## 40. Implementation phases

### Phase 0 — foundation

Create domain types, contracts, YAML parser, validation, DAG builder and application ports. Add golden JSON fixtures.

No executor yet.

Acceptance criteria: valid/invalid pipeline fixtures, cycle detection, unknown dependencies, schema errors, deterministic DAG.

### Phase 1 — local runner

Implement SQLite stores, local executor, scheduler, logs, cancellation, artifacts and basic WebSocket events.

Acceptance criteria: a three-job DAG executes in parallel where possible; cancellation works; process restart recovers stale runs; artifacts are downloadable.

### Phase 2 — agent tools

Add `ci_*` built-ins to `infrastructure/tools/toolbox.go`, contracts, tool schemas and agent behavior tests.

Acceptance criteria: an agent can discover, validate, run, inspect, diagnose and retry a pipeline without shelling out to a separate CI command.

### Phase 3 — human UI

Add Runs, Job Detail, Pipeline Definition, Runners, Artifacts and Cache views.

Acceptance criteria: every operation performed through UI maps to an application use case also callable by the agent.

### Phase 4 — Docker executor

Add isolated container execution with explicit mounts, limits and network configuration.

Acceptance criteria: a pipeline can choose Docker, job output and artifacts behave identically to local executor, and the UI clearly shows the isolation mode.

### Phase 5 — remote runner protocol

Introduce a runner daemon/agent protocol. Scheduler remains local to NusaShell initially; remote runner claims jobs through a secure connection.

Acceptance criteria: runner capabilities, heartbeats, leases, cancellation and logs work across a network.

### Phase 6 — autoscaling

Only after remote runners prove useful. Add a runner provider interface analogous to GitLab's separation between runner manager and autoscaler.

## 41. Testing strategy

Follow the repository's existing testing philosophy: Go unit tests, race tests, frontend Node tests, and handler-level tests against real HTTP/WebSocket/SSE paths. The existing verification baseline already uses `go test -race`, `go vet`, `go build`, and frontend tests. fileciteturn4file0L2-L2

Add tests at four levels.

Domain tests:

- DAG construction
- cycle detection
- status transitions
- retry rules
- artifact validation
- cache key generation

Application tests:

- scheduler parallelism
- dependency failure propagation
- cancellation
- stale lease recovery
- runner matching
- artifact handoff

Infrastructure tests:

- YAML parser
- SQLite persistence
- local executor
- log persistence
- artifact archive extraction

Transport/E2E tests:

- `ci.runs.start` over `/rpc`
- live WebSocket events
- reconnect and run state recovery
- UI run flow
- cancel flow
- retry flow

Use fake executors for scheduler tests. Do not spawn real processes in every unit test.

## 42. Suggested golden fixtures

Add:

```text
contracts/fixtures/ci_pipeline_basic.json
contracts/fixtures/ci_pipeline_dag.json
contracts/fixtures/ci_pipeline_invalid_cycle.json
contracts/fixtures/ci_run_status.json
contracts/fixtures/ci_job_status.json
contracts/fixtures/ci_events.json
```

The fixtures should make the wire contract stable before the frontend is implemented.

## 43. Observability

NusaShell already has bounded log persistence. CI should use a bounded operational log for scheduler events while job logs remain in dedicated CI storage. fileciteturn0file0L80-L96

Every run should have:

```text
created_at
started_at
finished_at
duration
runner_id
executor
workspace
pipeline_hash
status
```

Every job should have:

```text
queued_at
started_at
finished_at
duration
runner_id
exit_code
failure_reason
```

This is enough for useful local diagnostics without building a full telemetry platform.

## 44. What not to implement initially

Do not implement:

- GitHub Actions YAML compatibility.
- GitLab `.gitlab-ci.yml` compatibility.
- marketplace actions.
- arbitrary third-party actions executed from remote repositories.
- Kubernetes executor.
- cloud autoscaling.
- organization/team permissions.
- hosted runner billing/quotas.
- distributed cache federation.
- full expression language.
- reusable workflow inheritance system.
- dynamic workflow generation from arbitrary scripts.

These features create large security and maintenance surfaces without improving the first local NusaShell use case.

## 45. Recommended relationship with the existing GitHub Actions workflow

The repository's existing `.github/workflows/ci.yml` should remain authoritative for GitHub-hosted CI. NusaShell CI is a local execution layer, not a replacement for GitHub Actions.

The current workflow has three logical responsibilities: frontend tests, cross-platform backend verification, and cross-platform builds. fileciteturn3file0L2-L2 Those responsibilities can be represented in `.nusashell/pipeline.yaml` for local execution, but the two definitions should not be automatically synchronized in phase 1.

Later, a small `ci parity` tool can compare the intent of the two configurations, but full semantic equivalence is not a goal.

## 46. Recommended first NusaShell pipeline

The project's current verification baseline maps naturally to:

```yaml
version: 1
name: NusaShell verification

jobs:
  frontend:
    name: Frontend tests
    steps:
      - name: Install dependencies
        run: npm ci
      - name: Run frontend tests
        run: node --test frontend/*.test.mjs

  backend:
    name: Backend tests
    steps:
      - name: Format
        run: gofmt -l .
      - name: UI docs
        run: go run ./cmd/scan-ui-docs -check
      - name: Vet
        run: go vet ./...
      - name: Race tests
        run: go test -race ./...

  build:
    name: Build
    needs: [frontend, backend]
    steps:
      - name: Build
        run: go build ./...
```

This deliberately reflects the repository's current verification commands instead of inventing a second quality gate. The existing Makefile and GitHub workflow already provide the baseline. fileciteturn3file0L2-L2 fileciteturn4file0L2-L2

## 47. Implementation checklist

- [ ] Add `domain/ci_*.go` entities and state transitions.
- [ ] Add `application/ci_service.go` and scheduler.
- [ ] Add CI ports for stores, runner registry and executor.
- [ ] Add YAML parser and validator.
- [ ] Add DAG/cycle detection.
- [ ] Add SQLite CI persistence.
- [ ] Add local executor with process-group cancellation.
- [ ] Add log chunk persistence and live events.
- [ ] Add artifact storage.
- [ ] Add cache storage.
- [ ] Add `ci.*` RPC methods.
- [ ] Add `ci.*` WebSocket events.
- [ ] Add `ci_*` built-in agent tools.
- [ ] Add tool schemas and agent tests.
- [ ] Add Runs UI.
- [ ] Add Job Detail UI.
- [ ] Add Pipeline Definition UI.
- [ ] Add Runner UI.
- [ ] Add Artifact UI.
- [ ] Add Cache UI.
- [ ] Add Docker executor.
- [ ] Add cross-platform executor tests.
- [ ] Add E2E run/cancel/retry tests.
- [ ] Add UI documentation map entries and generated docs.
- [ ] Extend `make check` with CI-specific verification.

## 48. Final architecture decision

The recommended NusaShell CI architecture is a **local DAG scheduler + pluggable runners + structured agent tools + shared human UI**.

The scheduler belongs in `application`. Domain contains the pipeline/run state machine. Infrastructure contains YAML loading, SQLite, artifact/cache storage, and executors. Transport exposes RPC and WebSocket events. The existing Toolbox exposes a deliberately small CI tool surface to the model. The frontend renders the exact same run state used by the agent.

GitLab contributes the strongest concepts for executor abstraction, runner capabilities, DAG scheduling, artifacts/cache separation, and future autoscaling. GitHub Actions contributes the strongest concepts for workflow/job/step ergonomics, reusable abstractions, runner labels/groups, action-like step composition, and structured runner communication. citeturn0search8turn1search3turn1search1turn0search7turn0search6

NusaShell should copy these concepts, not their entire products.

The resulting system stays consistent with the existing NusaShell architecture: local, embedded, Go-first, vanilla-JS frontend, structured RPC/tools, event-driven UI, and minimal operational infrastructure. fileciteturn0file0L1-L35
