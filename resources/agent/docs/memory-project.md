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

Ops that exist as automation-learning scripts (`memory-query.sh`,
`memory-list.sh`, `memory-lint.sh`, `memory-audit.sh`, `memory-gate.sh`,
`memory-pattern-track.sh`, `memory-path.sh`, `memory-script-path.sh`)
return that script's stdout (and fail when the script would exit non-zero).
`admit` / `skip` / `archive` / `read` are NusaShell write/read helpers on
top of the same files.

## Admission, not mandatory writing

Make an admission decision before finishing a repository task. Writing
nothing is normal. Use `op=skip` with a concise reason when nothing
passes, then `op=gate` (with the same reason as `--no-update`) so lint
still runs:

1. it will help a later, different task;
2. it should remain true beyond this task;
3. it changes a decision, prevents a mistake, or shortens diagnosis;
4. there is no better source of truth (or memory can point at that source).

Do not store feature-completion notes, one-off test results, commit
summaries, or facts obvious from the repo. Never store user preferences
or profile facts here — those belong in `memory/user.md` or structured memory records.

## Ops

| op | Args | Script / notes |
| --- | --- | --- |
| `query` | `topic?`, `kind?`, `related?`, `id?`, `archive?`, `full?`, `limit?` | `memory-query.sh`. AND selectors; at least one required. Compact TSV `id\tkind\tfile\tscope`; `full=true` returns anchored bodies. `--related` is inbound **or** outbound. |
| `list` | | `memory-list.sh`. Absolute paths, guardrails first, then other live `*.md`, then `archive/`. |
| `read` | `kind` or `id` | Full entry or whole kind file. |
| `admit` | `kind`, `content`, `id?` | Upsert by ID, wrap `BEGIN_ENTRY`/`END_ENTRY`, lint, roll back on lint failure. Failure text is `memory-lint.sh` stdout (`LINT FAIL [...]` lines, then `memory-lint: N issue(s) found.`). Debug admits also pattern-track. |
| `skip` | `reason` | Negative admission. No disk write. |
| `archive` | `id` | Move a live entry to `archive/{kind}.md`. |
| `lint` | `kind?` | `memory-lint.sh [kind]`. Clean: `memory-lint: clean`. Issues: same stdout as the script (error). |
| `audit` | | `memory-audit.sh` health report. |
| `gate` | `reason?` | `memory-gate.sh [--no-update reason]`. Pattern-tracks debug/deploy, lints, then the worktree heuristic. `reason` is `--no-update`. |
| `pattern_track` | `kind` | `memory-pattern-track.sh <kind>`. |
| `path` | `kind`, `create?` | `memory-path.sh [--create]`. Prints `{base}/{key}/{kind}.md`. |
| `script_path` | `name`, `create?` | `memory-script-path.sh [--create]`. Prints `{base}/{key}/scripts/{name}`. |

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
