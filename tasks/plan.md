# Plan: Centralize pipeline definitions

## Approach

Remove the workspace-pipeline concept entirely. Pipeline YAML files live
under `<datadir>/ci/pipelines/*.yaml`, auto-discovered on boot and
registered in the existing automation registry (WorkflowStore). The
`ci_pipeline` dispatcher is removed — `automation_*` tools cover all
operations. `ci_run` drops `workspace`, only uses `workflow_id`.

## Phases

### Phase 1: New storage layer (add before remove)

1. Add `PipelineDirStore` port (application) + `DirPipelineStore` adapter
   (infrastructure) — reads `<datadir>/ci/pipelines/*.yaml`.
2. Add `DiscoverPipelines` use case — scan dir, parse, upsert into
   WorkflowStore with `source.kind=file`.
3. Wire in `wire.go` — create dir, call DiscoverPipelines on boot.

### Phase 2: Remove old workspace pipeline path

4. Remove `FilePipelineStore` adapter + `PipelineFileStore` interface +
   `Automation.Files` field + `ReadPipeline` + `StartPipeline` methods.
5. Remove `ci_pipeline` dispatcher family from `tool_dispatch.go`.
6. Remove `executePipelineOp` + `ci_pipeline_*` routing from `toolbox.go`.
7. Update `ci_run` in toolbox.go — drop `workspace` branch, only
   `workflow_id`.

### Phase 3: Contracts + RPC handlers

8. Remove `MethodCIPipelines*` constants, `CIWorkspaceRequest`,
   `CIPipelineReadResult` from contracts. Update `rpc_test.go`.
9. Update `ci_handlers.go` — remove `ci.pipelines.*` cases, update
   `ci.runs.start` to not use workspace, update `ci.runs.list` to drop
   workspace filter.

### Phase 4: Frontend

10. Remove `auto-workspace` input from `index.html`. Update
    `automation.js` — remove `state.workspace`, `runPipeline()`, use
    `automation.run` for all workflows.

### Phase 5: Tests

11. Update all affected tests: dispatch_test.go, toolbox_test.go,
    tool_dispatch_test.go, ci_service tests, ci_handler tests,
    rpc_test.go, automation.test.mjs.

### Phase 6: Docs

12. Update `automation.md` (rewrite), `data-locations.md` (add
    `ci/pipelines/`), `tools.md` (remove ci_pipeline), `system.md`
    (if needed).
13. Update `ui-source/ui-map.json` + regenerate `ui-automation.md`
    via `make scan-ui-docs`.

### Phase 7: Verify

14. Run all gates: `gofmt`, `go test ./...`, `go test -race ./...`,
    `go vet ./...`, `go build ./...`, `scan-ui-docs -check`.

## Risks

- **automation.db schema**: no change — WorkflowStore already stores
  WorkflowDefinition. File-sourced workflows just have `source.kind=file`.
- **Backward compat**: users with `.nusashell/pipeline.yaml` get a
  deprecation log, not auto-migration.
- **Trigger registration gap (the original bug)**: fixed by design —
  DiscoverPipelines calls `Auto.EnableWorkflow` for each file-sourced
  workflow with non-manual triggers.
