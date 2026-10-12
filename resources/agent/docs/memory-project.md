# Project memory

Project memory is a **separate** store from the human profile documents
(`memory/user.md`, `memory/soul.md`) and structured memory records. It keeps
durable, reusable facts about the
**active workspace** in skill-compatible anchored markdown so later
tasks skip expensive rediscovery.

The tool is advertised whenever the project memory store is configured.
Until the user picks a workspace, the active workspace defaults to the
host home directory, so `memory_project` is usable from the first turn.
If it is not listed, do not call it.

Default files live under `{dataDir}/memory_project/{key}/`. Settings →
Project memory (`project_memory_base`, under Memory & search) can point
at `~/.memory` to share the same on-disk layout with other agents.

All ops act on the same on-disk store. `admit` / `skip` / `archive` /
`read` write and read the files directly; the remaining ops run the store's
checks (query, list, lint, audit, gate, pattern tracking) and return their
report, failing when the check fails.

## Admission, not mandatory writing

Make an admission decision before finishing a repository task. Writing
nothing is normal. Use `op=skip` with a concise reason when nothing
passes, then `op=gate` (with the same reason) so lint still runs:

1. it will help a later, different task;
2. it should remain true beyond this task;
3. it changes a decision, prevents a mistake, or shortens diagnosis;
4. there is no better source of truth (or memory can point at that source).

Do not store feature-completion notes, one-off test results, commit
summaries, or facts obvious from the repo. Never store user preferences
or profile facts here — those belong in `memory/user.md` or structured memory records.

## Ops

| op | Args | Behavior |
| --- | --- | --- |
| `query` | `topic?`, `kind?`, `related?`, `id?`, `archive?`, `full?`, `limit?` | AND selectors; at least one required. Compact TSV `id\tkind\tfile\tscope`; `full=true` returns anchored bodies. `related` matches inbound **or** outbound links. |
| `list` | | Absolute paths in read priority: guardrails first, then other live `*.md`, then `archive/`. |
| `read` | `kind` or `id` | Full entry or whole kind file. |
| `admit` | `kind`, `content`, `id?` | Upsert by ID wrapped in `BEGIN_ENTRY`/`END_ENTRY`, then lint; rolls back on lint failure (`LINT FAIL [...]` lines in the error). Debug admits also pattern-track. Re-admitting an existing ID collapses every block carrying it into one fresh entry, so duplicate pairs heal on the next admit instead of rolling back. |
| `skip` | `reason` | Negative admission. No disk write. |
| `archive` | `id` | Move a live entry to `archive/{kind}.md`. |
| `lint` | `kind?` | Lint report. Clean: `memory-lint: clean`; any issue fails the op with the report. |
| `audit` | | Store health report. |
| `gate` | `reason?` | Admission gate: pattern-tracks debug/deploy, lints, then checks whether working-tree changes required a memory update. Prints a `memory-gate: pass` or `memory-gate: FAIL` verdict; `reason` records why no update was needed. |
| `pattern_track` | `kind` | Recurrence analysis for one kind; suggests promoting stable procedures. |
| `path` | `kind`, `create?` | Prints `{base}/{key}/{kind}.md`; `create` also creates the file. |
| `script_path` | `name`, `create?` | Prints `{base}/{key}/scripts/{name}` — the store's shared-scripts location; `create` makes the directory. |

Canonical writable kinds: `index`, `guardrails`, `roadmap`, `playbook`,
`dev-access`, `decisions`, `debug`, `validation`, `touch-map`,
`patterns`. ID prefixes: `IDX-` `G-` `R-` `PB-` `DEV-` `D-` `BUG-`
`V-` `T-` `P-`. `decision` aliases to `decisions`. User-profile files
(`preferences`, `user-profile`) are rejected.

`TOPICS` is at most 3 lowercase kebab-case terms. `LINKS` is
`[relation:TARGET_ID, …]` using `validated_by`, `procedure`,
`constrained_by`, `explained_by`, `depends_on`, `blocks`, or
`related_to`, and every target ID must already exist in live memory or
archive.

Query before admit. Keep `index.md` as one `IDX-project` snapshot
(PURPOSE / LOCKS / CURRENT_STATE / ROUTES) — not a feature list.

Decision field order (admit `kind=decisions`):

    KIND: DECISION
    STATUS: ACTIVE
    SCOPE: <topic>
    TOPICS: [<1-3 lowercase-kebab topics>]
    LINKS: []
    DECISION: <what was chosen>
    ALTERNATIVES_REJECTED: <what else was considered, why not>
    CONSEQUENCES: <what this constrains later>
    SINCE: <YYYY-MM-DD>
    SUPERSEDES: []

Good examples:

    memory_project(op="query", kind="index")
    memory_project(op="query", topic="deploy", related="BUG-deploy-health")
    memory_project(op="skip", reason="implementation only; no durable cross-task knowledge")
    memory_project(op="gate", reason="implementation only; no durable cross-task knowledge")
    memory_project(op="lint")
    memory_project(op="admit", kind="debug", id="BUG-wrong-port", content="KIND: DEBUG\nSCOPE: local fixture health\nSYMPTOM: readiness probed 8080\nROOT_CAUSE: hardcoded port\nFIX: use the bound port\nREUSE: shortens local deploy diagnosis")
    memory_project(op="archive", id="BUG-wrong-port")

Bad examples:

    memory_project(op="admit", kind="debug", content="fixed the tests")   # not reusable; skip instead
    memory_project(op="admit", kind="preferences", content="User likes dark mode")  # user fact; use memory
    memory_project(op="admit", kind="debug", id="D-tradeoff", content="...")  # ID prefix must match kind
    memory_project(op="admit", kind="decisions", content="TOPICS: [a, b, c, d, e, f]")  # max 3 topics
    memory_project(op="admit", kind="decisions", content="LINKS: [related_to:D-missing]")  # dangling target
    memory_project(op="query")   # needs at least one selector
    memory_save(...)            # unknown tool; this family is memory_project + op
