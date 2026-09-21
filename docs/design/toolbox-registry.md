# Toolbox registry and family split

Refactor plan for `infrastructure/tools/toolbox.go`. Status: proposed, not
started. The dispatcher-family model in `tool-dispatchers.md` is unchanged by
this plan — only how the toolbox is *organised internally* changes.

## Problem

`toolbox.go` is 2458 lines and holds six responsibilities that grew together:

| Region | Size | What it is |
| --- | --- | --- |
| `Execute` (`toolbox.go:895`) | 712 lines | 24 typed-tool cases behind a 3-way pre-router (`exec`, `file_*`+`grep`/`find_file`/`show`, dispatch roots) |
| `executeFamily` (`toolbox.go:364`) | 495 lines | 16 op cases across 5 families, plus a private `root+"_"+op` routing key |
| `executeAutomation` (`toolbox.go:2140`) | 254 lines | the automation + automation_schedule ops |
| `ListTools` (`toolbox.go:235`) | 82 lines | every advertised schema literal inline |
| schema DSL (`toolbox.go:2040`+) | ~90 lines | `obj`/`props`/`str`/`arrObj`/`strEnum`/`freeObj` |
| web searcher helpers (`toolbox.go:100-198`) | ~100 lines | searcher construction and source naming |

Two consequences, both observed rather than theoretical:

1. **Advertisement and execution can drift.** Adding a tool means editing
   `Execute` *and* `ListTools`; for family ops it also means editing
   `dispatchFamilies` in `application/tools`. Nothing at compile time links
   the three, so a tool can be advertised without a handler or vice versa.
   The dead 15-name legacy guard list removed in `54c1768` was exactly this
   failure mode.
2. **Two schema DSLs.** `obj`/`props`/`str`… here and
   `pStr`/`pInt`/`pBool`/`pEnum`/`objSchema` in
   `application/tools/dispatch.go:236` describe the same JSON-schema shape.

## Target shape

One registry, one entry per tool, entry = definition + handler:

```go
type toolHandler func(ctx context.Context, argsJSON []byte) (string, error)

type toolEntry struct {
    info    application.ToolInfo   // advertised definition
    handler toolHandler            // nil = advertised but executed by the agent layer
    enabled func() bool            // nil = always advertised
}
```

`handler == nil` is a real case, not an oversight: `read_media` and
`generate_media` are advertised by this roster but executed in
`application/agent/agent_round_tools.go` (they return attachments and need
model capabilities the toolbox does not hold). The entry marks them so the
roster stays complete and the intent is explicit.

- `Execute` becomes a registry lookup plus the existing pre-router paths.
- `ListTools` becomes a registry iteration filtered by `enabled`.
- Each family gets a file holding its entries and its op handlers; adding a
  tool becomes a one-file change that cannot desync advertisement from
  execution.
- `executeFamily` becomes a ~6-case root router (`skill` → `executeSkillFamily`,
  …); each family file owns its small op switch. The op enum and validation
  stay in `application/tools/dispatchFamilies` (that is the advertised
  contract), so the existing `DispatchOp` loud-failure behaviour is kept.

Proposed file layout under `infrastructure/tools/`:

```
toolbox.go        Toolbox struct, registry assembly, Execute/ExecuteStreamed,
                  ListTools, Close, shared helpers
tool_registry.go  toolEntry, toolHandler, lookup plumbing
tools_mcp.go      mcp_install/server_add/register/enable/disable/unregister/
                  list, tool_list, tool_schema, mcp_search, mcp_call, contract_read
tools_web.go      web_search, web_fetch, web_answer + searcher helpers
tools_subagent.go delegate, subagent, subagent_steer/stop/wait, mergeOp
tools_todo.go     todo, ask_question, wait_until, sleep
family_skill.go   skill ops        family_memory.go  memory ops
family_docs.go    docs ops         family_conversation.go conversation ops
family_project_memory.go           family_automation.go (executeAutomation)
```

## Invariants (must not change)

- **Roster contents and order.** The provider prompt cache is keyed on the
  tool list; order and names must be byte-identical. The registry is an
  ordered slice, assembled in today's order.
- **Error strings.** `unknown tool %q`, `unknown <root> op %q`, the
  `depMissing` texts, and every tool's failure message.
- **Naming model.** Root+op only, no per-verb aliases (`tool-dispatchers.md`).
- **`ExecuteStreamed`** keeps its `exec` special case.
- **`TestAllAdvertisedFamilyOpsRoute`** stays green — it is the routing-level
  invariant that every advertised op reaches a handler.

## Steps

Each step is independently shippable and must leave the suite green.

0. **Freeze the contract first.** Add a golden test pinning `ListTools()`
   names and order for (a) a bare toolbox and (b) a fully configured one
   (ACP + delegate, web_answer searcher, all three media modes). Today no
   test locks order, so every later step is otherwise unverifiable against
   the prompt-cache invariant. *(S)*
1. **Registry plumbing and typed-tool migration.** Introduce
   `toolEntry`/`toolHandler`, assemble the registry, rewrite `Execute` and
   `ListTools` on top of it, and move every typed-tool case out of `Execute`
   into per-cluster files (`tools_mcp.go`, `tools_web.go`,
   `tools_subagent.go`, `tools_todo.go`, `tools_media.go`). Decide the
   `file_*` question here: register the 12 file tools individually from
   `fileToolInfos()` or keep the prefix branch as an explicit pre-router
   path. *(L — one owner, `toolbox.go` is shared)*
2. **Split `executeFamily`** into per-family files with the root router left
   behind. *(M)*
3. **Move `executeAutomation`** into `family_automation.go`. *(S)*
4. **Split `files.go`** into per-tool functions (`file_read`, `file_patch`, …),
   mirroring `grep.go`/`show.go`/`find_file.go`. *(M)*
5. **Unify the schema DSL.** Deliberately last: it rewrites every schema
   literal, so doing it after the split lets each file be converted
   independently. The shared builder must live in a leaf package both
   `application/tools` and `infrastructure/tools` can import (the dependency
   rule forbids `application` → `infrastructure`). Descriptions must stay
   byte-identical — the step-0 golden is the guard. *(M)*
6. **Optional, last:** group the store ports on `Toolbox` into one named
   sub-struct. Deliberately deferred — it rewrites ~50 access sites plus the
   two construction sites (`cmd/nusashell/main.go`,
   `transport/harness_test.go`) for cosmetic gain only.

Step 1 is the serial bottleneck; steps 2-4 are disjoint files and can run as
parallel workstreams once it lands.

## Risks and decisions

- **Roster order is the whole risk.** Assembly order in the registry must
  reproduce today's list exactly; the step-0 golden test is the guard.
- **`enabled` predicates** must reproduce the current conditional
  advertisement: `subagent` (ACP agents or delegate configured),
  `web_answer` (searcher can answer), `generate_media` (any media mode
  configured). The "fully configured" golden covers all three.
- **`executeAutomation` nil guard** must keep returning
  `"automation is not configured"` for every family op — that string is
  asserted by the routing test.
- **The private routing key** (`root+"_"+op`) stays internal to
  `executeFamily` and must not escape into any handler signature.
- **Do not change** the family model, the op enums, or the wire shapes; this
  plan is internal reorganisation only.

## Verification per step

```
gofmt -l .
go build ./...
go vet ./...
go test ./infrastructure/tools/... ./application/... ./transport/... ./cmd/... -count=1
```

plus `go test ./...` before each commit. `transport` and `application` must be
included because `transport/harness_test.go` constructs a `Toolbox` and
`application` exercises the tool roster.

## Done when

- `toolbox.go` holds no tool implementation beyond the router, registry
  assembly, and shared helpers.
- Adding a typed tool touches exactly one file, and `ListTools` cannot
  advertise a tool that `Execute` cannot run.
- Roster golden, `TestAllAdvertisedFamilyOpsRoute`, and the full suite pass.
