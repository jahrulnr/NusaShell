# Project memory

Project memory is a **separate** store from user memory (`memory` /
`primary.md` + fragments). It keeps durable, reusable facts about the
**active workspace** in skill-compatible anchored markdown so later
tasks skip expensive rediscovery.

The tool is advertised only when the conversation has a workspace. If
it is not listed, do not call it.

Default files live under `{dataDir}/memory_project/{key}/`. Settings
`project_memory_base` can point at `~/.memory` to share the same on-disk
layout with other agents.

## Admission, not mandatory writing

Make an admission decision before finishing a repository task. Writing
nothing is normal. Use `op=skip` with a concise reason when nothing
passes:

1. it will help a later, different task;
2. it should remain true beyond this task;
3. it changes a decision, prevents a mistake, or shortens diagnosis;
4. there is no better source of truth (or memory can point at that source).

Do not store feature-completion notes, one-off test results, commit
summaries, or facts obvious from the repo. Never store user preferences
or profile facts here — those belong in `memory` (primary/fragments).

## Ops

| op | Args | Notes |
| --- | --- | --- |
| `query` | `topic?`, `kind?`, `related?`, `id?`, `archive?`, `full?`, `limit?` | AND selectors; at least one required. Compact rows by default; `full=true` returns anchored bodies. `--related` is inbound **or** outbound and excludes the related ID itself. |
| `list` | | Kind files in read priority, then other `*.md`, then `archive/`. |
| `read` | `kind` or `id` | Full entry or whole kind file. |
| `admit` | `kind`, `content`, `id?` | Upsert by ID into the canonical kind file, wrap `BEGIN_ENTRY`/`END_ENTRY`, lint, roll back on lint failure. Debug admits also pattern-track. |
| `skip` | `reason` | Negative admission. No disk write. |
| `archive` | `id` | Move a live entry to `archive/{kind}.md`. |
| `lint` | | Read-only report for the active key. |

Canonical writable kinds: `index`, `guardrails`, `roadmap`, `playbook`,
`dev-access`, `decisions`, `debug`, `validation`, `touch-map`,
`patterns`. ID prefixes: `IDX-` `G-` `R-` `PB-` `DEV-` `D-` `BUG-`
`V-` `T-` `P-`. `decision` aliases to `decisions`. User-profile files
(`preferences`, `user-profile`) are rejected.

Query before admit. Keep `index.md` as one `IDX-project` snapshot
(PURPOSE / LOCKS / CURRENT_STATE / ROUTES) — not a feature list.

Good examples:

    memory_project(op="query", kind="index")
    memory_project(op="query", topic="deploy", related="BUG-deploy-health")
    memory_project(op="skip", reason="implementation only; no durable cross-task knowledge")
    memory_project(op="admit", kind="debug", id="BUG-wrong-port", content="KIND: DEBUG\nSCOPE: local fixture health\nSYMPTOM: readiness probed 8080\nROOT_CAUSE: hardcoded port\nFIX: use the bound port\nREUSE: shortens local deploy diagnosis")
    memory_project(op="archive", id="BUG-wrong-port")

Bad examples:

    memory_project(op="admit", kind="debug", content="fixed the tests")   # not reusable; skip instead
    memory_project(op="admit", kind="preferences", content="User likes dark mode")  # user fact; use memory
    memory_project(op="admit", kind="debug", id="D-tradeoff", content="...")  # ID prefix must match kind
    memory_project(op="query")   # needs at least one selector
    memory_save(...)            # unknown tool; this family is memory_project + op
