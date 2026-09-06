# Application subsystem migration map

Status: **all steps moved**. `plugins/`, `logs/`, `telemetry/`, `settings/`,
`pets/`, `skills/`, `conversation/`, `provider/`, `media/`, `memory/`,
`automation/`, `learn/`, `tools/`, `subagent/`, and `agent/` now own
handlers behind a `Deps` Service (or facade Dispatch); root `App.Dispatch`
is thin routing. RPC table helpers live in `pkg/rpcdispatch` so feature
packages do not import the root application package.

Root leftovers are wiring, aliases, and media describe/hydrate — not a
further subsystem split.

## Target tree and boundary contract

```
application/
  app.go app_lifecycle.go app_runtime.go bus.go ports.go
  context.go errors.go rpc_dispatch.go feature_services.go
  *_wrappers.go          # root wiring + event spine + strangler aliases
  agent/       subagent/  tools/  conversation/  memory/  learn/
  skills/      automation/ plugins/ provider/  media/  logs/
  telemetry/   pets/     settings/
```

Rules enforced by the target state:

1. Feature packages never import each other. Cross-feature flow goes through
   Bus events (reactive) or the `agent` orchestrator (procedural).
2. Feature packages receive narrow Deps structs from the root wiring, never
   `*App`. Define consumer-side ports locally; infrastructure satisfies them.
3. `agent` and `subagent` may import feature packages only through their
   exported entry points (emit event, enqueue job, render result).
4. No `common/` or `shared/` package. Shared helpers go to `domain`, `pkg`,
   or stay duplicated until a real responsibility emerges.
5. Root `ports.go` dissolves during migration: each port moves to its
   consumer package. `rpc_dispatch.go` shrinks to thin per-package routing.
6. While files still live in the root package, do not add new cross-subsystem
   receivers; new code goes directly into its subpackage.

## File map

| Target | Files |
| --- | --- |
| `agent/` | turn engine, round stream, hydration, compaction, prompts, announcements, capability registry, ask-question, headless, TPM/400 learning, conversation agent rules. App wrappers keep `*App` tests compiling. |
| `subagent/` | ACP `acp.*` RPC, SpawnSubagents, SpawnDelegate, persist, OnDone orchestration, delegate run registry. App wrappers: `deliverRunDone` / `complete*Locked` / `triggerBackgroundCompletionTurn` forward to `agent.Service`. |
| `tools/` | tool factory, dispatcher families, tool contracts, docs.* RPC, model override tool, pipeline ACP-filtered toolbox + PipelineAgentRunner, learner result tool advertisement. Root leftovers: `context.go` (aliases of `tools` keys), `tools_wrappers.go`. |
| `conversation/` | conversations, repository, messaging, todos, workspace, instruction files, orphan journals. |
| `memory/` | memory_dispatch, memory_handlers, memory_service, task_memory |
| `learn/` | experience recording, jobs, learner LLM, trajectory, edges/graph, search, lifecycle, `learning.*` / `experience.*` RPC. |
| `skills/` | skills_dispatch, skills_handlers |
| `automation/` | automation_handlers, automation_memory, automation_ports, automation_scheduler, automation_service, execution_scheduler |
| `plugins/` | plugin_dispatch, plugins_handlers |
| `provider/` | ai_convert, ai_dispatch, endpointscache, env_providers, provider_retry, providers, providers_endpoint_handler, providers_import, providers_models, rate_limit, slowdown |
| `media/` | audio_read_audio, audio_stt, document_read, generated_media, image_generate, offline_stt, offline_tts, speech_generate, stt_install, tts, tts_install, video_generate, video_read_video, vision_fallback, vision_read_image |
| `logs/` | logs_dispatch, logs_handlers |
| `telemetry/` | telemetry |
| `pets/` | pets_autostart, pets_install |
| `settings/` | settings_dispatch, settings_handlers, settings_watcher |
| root | app, app_lifecycle, app_runtime, bus, context, errors, ports, rpc_dispatch, feature_services, media_describe, attachment_hydrate, `*_wrappers.go` |

Each `*_test.go` moves with its file. Package-internal tests that reach
across future boundaries become cross-package tests (export what the test
needs or restructure the test to the consumer side).

## Flagged files (decision needed while moving)

- `context.go` — **stayed in root** as thin aliases of `application/tools`
  context keys so tools, todos, and agent share one key space without
  `agent` importing `nusashell/application`.
- `conversation_agent_rules.go` — **moved** to `application/agent`
  (`*agent.Service` host). `conversation` does not import `agent`.
- `tpm_learning.go` / `learned_params.go` — **moved** to `application/agent`.
  Learn stays out of the turn loop; experience recording is a Deps callback.
- `instruction_files.go` — workspace concern, consumed by hydration via
  `conversation.ListInstructionFiles`.
- `automation_memory.go` — in-process `AutomationStore` (workflows/runs/
  schedules), not `application/memory`. Lives in `application/automation`;
  that package must not import `application/memory`.
- `task_memory.go` — retrieval side of memory; mapped to `memory/` even
  though it publishes into conversations (via announcement, not import).
- `ai_convert.go` / `ai_dispatch.go` — the application-to-core bridge now
  lives in `application/provider` (the single sanctioned core/config
  import). Root aliases chat types and re-exports convert helpers.
- `learning_edges.go` / `learning_search.go` — **moved**. jsonstore imports
  replaced with consumer-side ports (`EmbeddingCache`, `NewKeywordIndex`).
  App wires `*jsonstore.EmbeddingCache` and a BM25 adapter in
  `learn_wrappers.go`.

## Migration order (strangler, one subsystem per change)

1. `plugins/` then `logs/` then `telemetry/` — **moved**.
2. `settings/`, `pets/`, `skills/` — **moved**.
3. `conversation/` — **moved** (repository, room RPC, todos, workspace
   listing, messaging, instruction files, orphan journals). Agent
   conversation rules live in `application/agent`.
4. `provider/` — **moved**. Chat types, convert/retry/rate-limit, and
   `ai.*` RPC live in `application/provider`. That package is the single
   sanctioned `application → infrastructure/ai/core` (and
   `infrastructure/config` kind-allowlist) import.
5. `media/` — **moved** (generation, TTS/STT + installers, read tools).
   Chat describe (`describeOneImage`/`Audio`/`Video`) and conversation
   enrich (`enrichWith*Descriptions`) stay on App: they need Factory +
   `completeWithRetry` + the conversation repository. `hydrateAttachmentDataURLs`
   / `chatMessagesForProvider` stay on App (chat path, not media generation).
   Root aliases keep `application.ImageGenRequest` etc. compiling for
   infrastructure. TTS/STT install RPC routes to `media.Service.Dispatch`.
6. `memory/` — **moved** (record RPC, consolidator Apply, task-memory
   announcements). Cross-feature flow is callbacks: `OnChanged` /
   `Announce` → App announcement publish; `PersistAnnounced` →
   conversation LastAnnouncedRecords; `OnRecordDeleted` → prune learning
   edges + invalidate searcher. Root aliases keep `MemoryRecordStore`,
   `LearningOpStore`, `NewMemoryService`, and handler wrappers compiling
   for jsonstore, toolbox, and learn jobs. `MemoryDocumentStore` is a
   root alias of `learn.DocumentStore` (jsonstore adapters and
   App.User / App.Agent still compile).
7. `automation/` — **moved** (ports, Automation facade + Dispatch, in-process
   AutomationStore, both schedulers). RPC `automation.*` routes to
   `Automation.Dispatch`; App injects itself as `HeadlessTurnRunner` for
   `runs.steer`. `Caps` is `CapabilityResolver` so the package does not
   import the concrete `CapabilityRegistry` (lives in `application/agent`;
   root alias `application.NewCapabilityRegistry` still compiles).
   Schedulers emit through a local `Emitter` (`*Bus` still assigns). Root
   aliases keep infrastructure and cmd compiling.
8. `learn/` — **moved** (experience recording, jobs, learner LLM,
   trajectory, edges/graph, search, lifecycle decay, `learning.*` /
   `experience.*` RPC via `learn.Service.Dispatch`). Cross-feature flow
   is injected ports: `ApplyMemory` / `RejectMemory` → memory.Service;
   `SkillCatalog` + `OnSkillChanged`; local `HeadlessTurnRunner` (App
   uses `AgentLearner`); `Go` → `a.goSafe`; BM25 via `NewKeywordIndex`.
   BUG-learner-supersede-payload-mismatch remains fixed
   (`opsFromLearnerConsolidate` writes supersede id on TargetID and
   Payload["id"]; memory.Apply reads payload then TargetID; batch
   continues after a rejected op). TPM/400 policy lives in
   `application/agent`. App-level tests stay in package
   `application` and call wrappers; helper tests that do not need `*App`
   live in `application/learn/`.
9. `tools/` — **moved** (factory, dispatcher families, tool contracts,
   docs.* RPC, model override tool, pipeline ACP-filtered toolbox +
   `PipelineAgentRunner`, learner `learn()` advertisement). Feature
   packages never import each other: `tools` does not import `learn`
   (`acknowledgeLearnerResult` stays a root wrapper calling
   `learn.ValidLearnerResult`); local `HeadlessTurnRunner` is
   `RunHeadlessTurn` only so tools does not import `automation`.
   `ToolFactory.Get` returns `[]tools.ToolInfo`; root `toToolDefs`
   converts to `provider.ToolDef` for the turn loop.
   `context.go` stays in root as aliases of `application/tools` keys.
   `turnToolDefs` is an App wrapper over `agent.Service.TurnToolDefs`.
10. `subagent/` — **moved** (ACP `acp.*` RPC via `subagent.Service.Dispatch`,
   SpawnSubagents, SpawnDelegate, persist + OnDone orchestration, delegate
   run registry on the Service so spawn + list share state). Feature
   packages never import each other: `subagent` uses injected ports only
   (`DeliverRunDone`, `CompleteSubagent`/`CompleteDelegate`, local
   `ObservedHeadlessTurn`, `Go` → `a.goSafe`). App.Dispatch `acp.` routes
   to `a.subagentService().Dispatch`. Root aliases keep
   `application.AcpSpawnRequest` / `AcpRuntime` / `AcpAgentStore` compiling
   for acpruntime and jsonstore. G-subagent-done-steer-boundary is
   unchanged: OnDone queues on a live parent TurnRun and injects
   immediately when idle.
   `deliverRunDone` / `complete*Locked` / `triggerBackgroundCompletionTurn`
   / `resolveConversationProvider` live on `agent.Service`; App wrappers
   remain so subagent OnDone and `acp_done_test.go` keep compiling.
   `onAcpRunDone` remains an App wrapper so `app.go` SetCallbacks compiles.
   App wrappers keep `SpawnSubagents` / `SpawnDelegate` for the toolbox.
11. `agent/` — **moved** (turn engine, round stream, hydration, compaction,
    prompts, announcements, capability registry, ask-question, headless,
    TPM/400 learning, conversation agent rules, background run-done
    delivery). Receivers are `*agent.Service`; App wrappers keep package
    `application` tests compiling. `dispatchAgent` is a thin switch:
    conversation/todos/workspace prefixes → `conversation.Service`;
    remaining turn/ask RPC → `agent.Service.Dispatch`. Media describe/
    hydrate stay on App and are injected via Deps. Agent may import
    `conversation`, `provider`, and `tools` exported entry points; it
    never imports `nusashell/application` or `infrastructure/ai/core`.

Verification per step: `gofmt`, `go vet`, `go test ./... -race`,
`node --test frontend/tests/*.test.mjs`, plus `make check` before closing
the change (the audit showed ad-hoc narrow gates are not sufficient).
