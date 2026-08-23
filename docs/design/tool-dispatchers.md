# Tool dispatcher families

One advertised tool per family instead of one tool per verb. `skill`,
`memory`, `docs`, and `ci_pipeline` are dispatched by a required `op` field
(`memory` + `op=save`), replacing 15 provider-facing schemas with 4. New
verbs cost an enum value, not a new schema — prompt growth per feature is
sub-linear.

## Single naming layer

Root+op is the ONLY form of these tools anywhere in the system:

- **Roster** — providers receive the family definitions
  (`DispatcherToolInfos()`) plus every non-family built-in.
  There are no per-verb names on any roster.
- **Persistence** — a call is stored exactly as emitted:
  `{name:"memory", args:{op:"save",…}}`. History, UI, and transcripts show
  the root form.
- **Execution** — `Toolbox.Execute` resolves root+op via `DispatchOp`
  (missing/unknown ops fail loud with the valid list) and routes to its
  handler. The resolved `root+"_"+op` string is a private routing key inside
  Execute and never escapes it.
- **Internal callers** — hydration checkpoints, the review agent, and tests
  call the same root form (`Execute(ctx, "memory", {"op":"list",…})`). No
  caller has a special naming dialect.
- **No aliases** — a call named like an old verb (`memory_save`,
  `docs_read`, …) is simply an unknown tool and fails loud. Pre-migration
  conversations keep their stored strings; replaying those specific calls
  errors loudly by design.

## Rules

- Families must be CRUD/search-shaped with simple params. Hot-path tools
  (`exec`, `file_*`, `web_*`, `mcp_call`), process-control verbs
  (`ci_run/wait/cancel/steer`), invariant-enforcing writers with distinct
  contracts (`artifact_create/update` — the frontend renders cards on those
  exact names), and privileged gates (`mcp_register/install/server_add`)
  stay as typed tools.
- Adding a verb: add the op to the family spec + its Execute case + tests.
- Classification sites that must not fail loud (learning mutations,
  whitelist ops) read the op via `OpArg`; execution paths validate via
  `DispatchOp`.
