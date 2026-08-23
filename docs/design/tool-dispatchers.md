# Tool dispatcher families

One advertised tool per family instead of one tool per verb. `skill`,
`memory`, `docs`, and `ci_pipeline` are dispatched by a required `op` field
(`memory` + `op=save`), replacing 15 provider-facing schemas with 4. New
verbs cost an enum value, not a new schema — prompt growth per feature is
sub-linear.

## Mechanism

Three small pieces, one source of truth (`application/tool_dispatch.go`):

1. **Roster compaction** — `CompactFamilies` runs only where the interactive
   provider roster is built (`App.toolDefinitions`). `Toolbox.ListTools`
   still returns full per-verb defs, so the review-agent whitelist and the
   pipeline `FilteredToolbox` keep their exact-name contracts untouched.
2. **Canonicalization** — before persistence and execution, the agent runner
   rewrites dispatcher calls to legacy names via `CanonicalizeToolCalls`.
   Conversation history, learning-event classification, untrusted-output
   wrapping, UI rendering, and every downstream consumer see stable legacy
   names without knowing dispatchers exist.
3. **Execution routing** — `Toolbox.Execute` resolves roots to per-op cases
   via `DispatchCanonical`. Missing or unknown ops fail loud with the valid
   list (self-describing error). The per-op names are internal canonical
   targets only (hydration checkpoints, review-agent replay): a model call
   that emits one directly is rejected loud with the exact `{root, op}`
   rewrite at the agent tool executor (`LegacyAliasError`) — the
   hidden-alias path was removed.

Args are preserved verbatim; the redundant `op` key is ignored by the legacy
handlers' strict structs.

## Rules

- Families must be CRUD/search-shaped with simple params. Hot-path tools
  (`exec`, `file_*`, `web_*`, `mcp_call`), process-control verbs
  (`ci_run/wait/cancel/steer`), invariant-enforcing writers with distinct
  contracts (`artifact_create/update` — the frontend renders cards on those
  exact names), and privileged gates (`mcp_register/install/server_add`)
  stay as typed tools.
- Adding a verb: add the op to the family spec + its Execute case + tests.
- Do not canonicalize inside provider handlers; there is exactly one choke
  point (the runner) plus one fallback (Execute) by design.
