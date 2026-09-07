---
name: workspace-organizing
description: Keep workspaces tidy and collision-free with purpose-based folder conventions (flat: notes/data/outputs/scripts/archive; project: projects/<slug>/{docs,assets,source,reports,research}), discovery before writing via memory_project, and kebab-case naming. Use whenever the agent creates, writes, moves, or renames files in a shared/team workspace, starts a multi-file task or named project, or the user asks to organize, clean up, restructure, audit, or find files.
metadata:
  source: "goclaw skills/workspace-organizing v1.2 (CC BY-NC 4.0 / Proprietary front matter) — concept rewritten for NusaShell"
  version: "2"
---

# Workspace organizing

Arrange files so they are predictable, collision-free, and findable again — by humans and by the next agent. This is a **discipline** skill: it runs before files are written (deciding placement + checking for duplicates) and when asked to tidy up or find things.

## Scope

Governs: new files in the active workspace, shared/team folders, and multi-file tasks. Does NOT govern: edits inside an existing project tree (a cloned repo), system paths, ephemeral files in the temp directory.

## Two modes — pick one per workspace root

**Mode A — Flat** (default for one-off work): notes/, data/, outputs/, scripts/, archive/, tmp/.

**Mode B — Project** (named work, ≥3 mixed files, or work spanning sessions): `projects/<slug>/` with docs/, assets/, source/, reports/, research/. Slug is descriptive kebab-case: `customer-churn-q2`.

| Signal | Mode |
|---|---|
| User names a project/campaign | B |
| Task produces ≥3 mixed files (docs + code + assets) | B |
| Work spans sessions / will be revisited | B |
| Single ad-hoc deliverable | A |
| Workspace already uses one mode | Match it |

Two projects = two folders under `projects/`. Flat mode never nests inside a project.

## Discovery before writing (REQUIRED for new files)

Before `file_write`/`exec` that produces a file in the workspace:

1. **Query memory_project first**: `memory_project` `op=query` with a short topic for the file — are there prior decisions or related files? (goclaw's vault_search equivalent; NusaShell has no vault, so memory_project + `find_file` are the discovery sources.)
2. If hits exist → decide: update the existing file, reference it, or genuinely write a new one.
3. `file_list` the target folder — confirm no name collision. On collision: pick a more specific name, or archive the old file first.
4. Only then write.

Skip discovery only for: pure `tmp/` files, archive moves, or when the user explicitly named the target path.

## Placement decision (ask in order)

1. Intermediate file re-read and discarded? → `tmp/` — do not deliver.
2. User will download/read this as the result? → `outputs/` (flat) or `projects/<slug>/reports/` (project).
3. Code to be executed? → `scripts/` (flat) or `projects/<slug>/source/`.
4. Generated media (image/video/audio)? → `assets/` (flat) or `projects/<slug>/assets/`.
5. Raw research material / structured data? → `data/` (flat) or `projects/<slug>/research/`.
6. Thinking/summary prose? → `notes/` (flat) or `projects/<slug>/docs/`.
7. Replacing a previous version? → move the old one to `archive/` first, then write the new one in its original folder.

When unsure: `notes/` (flat) or `projects/<slug>/docs/`. **Never write to the workspace root** except configuration files that genuinely live there.

## Naming rules

- Descriptive kebab-case: `customer-churn-analysis.md` — not `Analysis 1.md`.
- ISO date prefix (`2026-09-07-`) for time-sensitive files (meeting notes, status reports).
- Task slug when relevant: `pr-123-review.md`.
- No spaces or special characters other than `-` and `_`; lowercase; extension matches content.
- Forbidden standalone names: `untitled`, `output`, `result`, `test`, `temp`, `final` — if you cannot name it better, the file probably should not be written yet.

## Discoverability

- Files in `outputs/`, `reports/`, `docs/`, `notes/`, `research/` worth re-reading: write a clear one-line title + a descriptive intro paragraph so future `memory_project` queries and searches recall them. Reference named entities (projects, people, decisions) in the body.
- `tmp/`, `scripts/`, `data/`, `source/`: do not optimize for discovery — that is index noise.
- Canonical notes promoted from `notes/` → move to `outputs/` or `reports/` with a strong title.

## Shared/team workspaces

- Every write goes under `shared/<agent_key>/` unless writing to `shared/_common/` (only artifacts genuinely meant for the whole team; treat as append-mostly).
- Inside `shared/<agent_key>/`, apply flat or project mode (pick one).
- Team projects: `shared/_common/projects/<slug>/`, namespaced with an `<agent_key>-` prefix.
- **Never overwrite a file in another agent's namespace** (`shared/<other-agent-key>/...`) — reading is fine; writing/moving/deleting is not.
- Before writing in a shared root: `file_list` first + query memory_project.

## Per-scope rules

- **Personal**: apply the convention to NEW files. Do not reorganize files the user placed manually without an explicit request ("clean up", "organize", "tidy"). The user is the sole owner — respect their conventions if they have one.
- **Delegate/subagent**: follow the same conventions in the assigned workspace; end the task with a short manifest of created files so the caller knows what was produced and where.

## End-of-task manifest

After a complex task (≥3 files created, or any project-mode work), end the report with a list:

```
Created:
- outputs/report.md      ← main deliverable
- projects/landing/docs/spec.md
- scripts/prepare.py      ← executable
Archive:
- notes/2026-09-05-draft.md → archive/2026-09-05-draft.md
```

## Trigger

Use when: before writing files in a shared workspace, starting a named project, producing reports/assets/exports, delegating, or when the user says "messy", "where did I save it", "organize workspace", "find related files". NOT for: read-only operations, edits inside an existing project tree, or short-lived files deleted in the same turn.