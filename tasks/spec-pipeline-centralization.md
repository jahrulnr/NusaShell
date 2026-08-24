# Spec: Centralize pipeline definitions into NusaShell config

## Objective

Pipeline definitions currently live as `.nusashell/pipeline.yaml` in each
workspace — a 1:1 clone of GitHub/GitLab convention. This is wrong for
NusaShell: a pipeline is an automation owned by NusaShell, not a file that
travels with an arbitrary repo. Centralize pipeline definitions under the
NusaShell data directory so they are:

- Owned and managed by NusaShell (not scattered across project trees).
- Auto-discovered and registered with the scheduler on boot.
- Human-editable with any text editor (YAML files under the data dir).
- Unified with the existing automation registry — no dual "pipeline vs
  automation" concept.

Success criteria:

1. `ci_pipeline` dispatcher tool is removed from the roster.
2. `ci_run` no longer accepts a `workspace` parameter; it only uses
   `workflow_id`.
3. `FilePipelineStore` / `PipelineFileStore` (workspace-based) are removed.
4. Pipeline YAML files are discovered from `<datadir>/ci/pipelines/*.yaml`
   on boot and registered in the automation registry.
5. `automation_list` / `automation_read` / `automation_run` work for
   file-sourced pipelines without changes.
6. `automation_enable` / `automation_disable` work for file-sourced
   pipelines (upserts the parsed definition into WorkflowStore, registers
   triggers with the scheduler).
7. Frontend "Run pipeline" + workspace input is replaced by picking a
   workflow from the list and clicking Run.
8. All docs, tool descriptions, UI map, and tests reflect the new model.
9. `gofmt`, `go test ./...`, `go test -race ./...`, `go vet ./...`,
   `go build ./...` pass.

## Tech Stack

- Go (existing NusaShell codebase, clean architecture).
- SQLite (automation.db — existing WorkflowStore for scheduler index).
- YAML files on disk (pipeline definitions — source of truth for
  file-sourced workflows).
- Native JS frontend (no build step).

## Commands

```text
Build:         go build ./...
Test:          go test ./...
Race:          go test -race ./...
Vet:           go vet ./...
Format:        gofmt -l .
UI docs scan:  go run ./cmd/scan-ui-docs -check
```

## Project Structure (files touched)

```text
domain/
  workflow.go              — WorkflowSource.Kind gains "file" (already exists)
application/
  ci_ports.go              — remove PipelineFileStore; add PipelineDirStore
  ci_service.go            — remove Files field, ReadPipeline, StartPipeline;
                             add DiscoverPipelines (scan dir + upsert)
  ci_handlers.go           — remove ci.pipelines.* handlers; ci.runs.start
                             drops workspace
  tool_dispatch.go         — remove ci_pipeline dispatcher family
infrastructure/
  ci/
    memory_store.go        — remove FilePipelineStore; add DirPipelineStore
                             (reads <datadir>/ci/pipelines/*.yaml)
    wire.go                — wire DirPipelineStore; call DiscoverPipelines
                             on boot
  tools/
    toolbox.go             — remove ci_pipeline handler + executePipelineOp;
                             ci_run drops workspace branch
    toolbox_test.go        — update tests
    dispatch_test.go       — remove ci_pipeline routing cases
contracts/
  ci.go                    — remove MethodCIPipelines*, CIWorkspaceRequest
                             (keep for runs.list workspace filter? no —
                             remove workspace from runs too), 
                             CIPipelineReadResult
  rpc_test.go              — update method list
frontend/
  index.html               — remove auto-workspace input
  js/views/automation.js   — remove workspace state + runPipeline; use
                             automation.run for all workflows
resources/agent/docs/
  automation.md            — rewrite: no workspace pipeline, centralized
  data-locations.md        — add ci/pipelines/ dir, remove workspace note
  tools.md                 — remove ci_pipeline from roster
  ui-automation.md         — regenerate (auto-workspace removed)
  ui-source/ui-map.json    — update map
application/prompts.go     — no change (loads system.md)
resources/agent/prompts/
  system.md                — update if it mentions ci_pipeline/workspace
```

## Code Style

Follow existing NusaShell conventions:

- Clean architecture: domain → application → infrastructure.
- Ports (interfaces) in `application/`, adapters in `infrastructure/`.
- Inject clocks, stores, filesystems for testability.
- YAML frontmatter not needed for pipeline files — they are full
  WorkflowDefinition YAML (same schema as automation_save).
- File names: `<name>.yaml` where `<name>` becomes the workflow ID
  (`pipeline:<name>` or just `<name>` — decide during implementation).

Example pipeline file at `<datadir>/ci/pipelines/deploy.yaml`:

```yaml
name: Deploy
triggers:
  - every:
      cron: "0 12 * * *"
      timezone: Asia/Jakarta
jobs:
  build:
    steps:
      - run: make build
  deploy:
    needs: [build]
    steps:
      - run: make deploy
```

## Testing Strategy

- **Unit tests**: DirPipelineStore (parse, discover, skip corrupt),
  DiscoverPipelines (upsert, idempotent), ci_run without workspace.
- **Integration tests**: boot automation with a pipelines dir, verify
  file-sourced workflow appears in automation_list, triggers registered.
- **Test updates**: remove all ci_pipeline dispatcher tests, remove
  workspace-based StartPipeline/ReadPipeline tests, update ci_run tests
  to only use workflow_id.
- **Race**: `go test -race ./application/ ./infrastructure/ci/
  ./infrastructure/tools/ ./transport/`.

## Boundaries

- **Always**: TDD (red → green → refactor), update docs in same change,
  run all gates before declaring done.
- **Ask first**: changing WorkflowStore schema, adding new RPC methods,
  changing automation.db migration.
- **Never**: delete tests to make suite pass, commit secrets, break
  automation.db backward compat without migration.

## Success Criteria

1. `ci_pipeline` tool removed from roster and handlers.
2. `ci_run` only accepts `workflow_id` (no `workspace`).
3. Pipeline files discovered from `<datadir>/ci/pipelines/*.yaml`.
4. File-sourced workflows appear in `automation_list` and are runnable
   via `automation_run` / `ci_run` with `workflow_id`.
5. Triggers in file-sourced pipelines are active (scheduler registers
   them on boot).
6. Frontend has no workspace input; all workflows run from the list.
7. All docs + UI map regenerated and consistent.
8. All gates pass: `gofmt`, `go test ./...`, `go test -race ./...`,
   `go vet ./...`, `go build ./...`.

## Open Questions

1. **Workflow ID for file-sourced pipelines**: use filename without
   extension (e.g. `deploy.yaml` → ID `deploy`), or prefix with `file:`
   (e.g. `file:deploy`)? Filename is simpler and matches the user's
   mental model. → **Decision: filename, no prefix.** If collision with
   a DB-sourced workflow, file wins (file is source of truth).

2. **Hot-reload**: should editing a pipeline file mid-session re-register
   triggers, or only on boot? Boot-only is simpler and safe. Hot-reload
   can be added later. → **Decision: boot-only for now.** Document that
   restart picks up changes.

3. **`ci.runs.list` workspace filter**: currently filters runs by
   workspace. Since workspace is no longer a concept, remove the filter
   (list all runs). → **Decision: remove workspace from RunFilter.**
   Runs still carry a `workspace` field for executor scratch dir, but
   it's not a filter dimension anymore.

4. **Migration**: existing users with `.nusashell/pipeline.yaml` in
   projects — should NusaShell auto-migrate them to the data dir on
   first boot? → **Decision: no auto-migration.** Log a one-time
   deprecation warning if `.nusashell/pipeline.yaml` is found in the
   CWD. User moves the file manually.
