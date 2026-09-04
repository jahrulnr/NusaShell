# Skills

A skill is a markdown instruction pack the agent can load on demand. Each skill
lives in its own directory `<datadir>/skills/<id>/SKILL.md` plus optional
support files (references, scripts, assets).

## Ownership and priority

Every skill has an `owned_by` field that records who provided it:

- `user` — authored by the user via the Skills workspace or `skill(op="save")`.
- `builtin` — shipped with NusaShell and seeded into the data directory.
- `plugin:<plugin-id>` — bundled inside a plugin's `skills/` directory and
  mounted read-only at plugin install time. Uninstalling the plugin removes
  the skill.

When two owners define a skill with the same ID, the higher-priority owner
shadows the lower one. Priority order: `user` > `builtin` > `plugin:<id>`.
Shadowed skills still appear in `skill(op="list")` (dimmed in the UI) but
the agent's skill resolution and the file path below use the winner.

## Authoring

Each skill has a name, a short description and markdown content. The
description is what the agent sees in the tool listing, so make it specific:
what the skill does and when to use it.

Keep instructions imperative and self-contained; the content is injected
verbatim into the agent's context when the skill is loaded.

Plugin-owned skills are read-only — uninstall the plugin to remove or modify
them.

## Using skills

The `skill` dispatcher tool handles catalog and authoring; `op` selects:

- `list {limit?}` — enumerate skills. Returns `owned_by` and `shadowed` flags
  per entry; use `owned_by` to resolve the skill directory (see Reading skill
  files below).
- `search {query,limit?}` — discovery-only name/description search. Results
  include metadata (`id`, `name`, `description`, `owned_by`) but never the
  SKILL.md body.
- `save {name,content,description?,id?,path?}` — create or update a
  user-owned skill, or write a support file when `path` is set (the skill
  must already exist).
- `delete {id,owned_by?}` — remove a skill. Plugin-owned skills cannot be
  deleted directly (uninstall the plugin instead). The background learning
  agent is instructed to only delete agent-owned skills; user-owned and
  builtin skills are protected by this guidance.

The Skills workspace is a read-only browser: it shows the catalog, the file
tree of the selected skill, and a file viewer. Use `skill(op="save")` (or the
New skill button) to author skills.

## Reading skill files

Skill content lives on disk as plain files. Read SKILL.md and support files
with `file_read`; list a skill folder with `file_list`. The `skill` tool no
longer has `read` or `files` ops — file I/O is the file tools' job.

Path layout (the data directory is documented in `docs(op="read",
id="data-locations")`):

| Owner | Directory |
| --- | --- |
| `user` / `builtin` | `<datadir>/skills/<name>/` |
| `plugin:<plugin-id>` | `<datadir>/plugins/<plugin-id>/skills/<name>/` |

Each skill directory contains `SKILL.md` plus optional support files
(`references/…`, `scripts/…`, `templates/…`).

Workflow:

1. `skill(op="list")` (or `op="search"`) — find the skill. Each result
   includes a `path` field with the absolute path to the skill directory
   on disk, and a `bundled` flag (`true` when the skill has support files
   beyond `SKILL.md` — e.g. `references/`, `templates/`, `scripts/`,
   `examples/`). Use `path` directly; do not construct the path from the
   table below. Search is only discovery; it does not load instructions.
2. `file_read` the absolute `SKILL.md` (append `/SKILL.md` to the `path`
   from step 1) before applying the skill's instructions. When
   `bundled=true`, run `file_list` on `path` first to discover support
   files; when `bundled=false`, skip `file_list` — the skill is a single
   `SKILL.md` with no subfiles. Read a support file by appending its
   relative path to `path` when needed.

Good example:

    skill(op="search", query="release checklist")
    # → {"name":"release-checklist","owned_by":"user","path":"/home/user/.config/nusashell/skills/release-checklist", ...}
    file_read(path="/home/user/.config/nusashell/skills/release-checklist/SKILL.md")

Bad examples:

    skill(op="read", name="release-checklist")  # op no longer exists
    file_read(file_path="~/.config/nusashell/skills/release-checklist/SKILL.md")  # ~ not expanded; use an absolute path

### Saving skills guidance

Save class-level, reusable procedures — never one-off debugging notes or
environment-failure folklore.

Good example:

    skill(op="save", name="release-checklist",
          description="Steps for tagging a release and verifying the build",
          content="## Release\n1. …")

Bad examples:

    skill(op="save", name="bug-2026-08-19", description="fix", content="the debug session…")

    skill(op="save", name="my-api-key-notes", description="credentials", content="sk-…")  # secrets

## Builtin automation-authoring skill

`automation-authoring` is the builtin cookbook for YAML workflows. Use it for
pipeline, Telegram trigger, alarm, reminder, and schedule requests. Its
support files are seeded with the skill and can be read progressively:

    skill(op="search", query="automation authoring")
    file_list(path="<datadir>/skills/automation-authoring/templates")
    file_read(path="<datadir>/skills/automation-authoring/templates/alarm-once.yaml")

The `templates/` folder contains concrete starting points; `references/`
contains the YAML contract, supported event variables, and MCP discovery
flow. Adapt a template, call `automation(op="validate")`, then save it with
`automation(op="create", enabled=false)` until the user confirms activation.

## Builtin hatch-pet skill

`hatch-pet` is the pet/mascot authoring pipeline for NusaShell desktop pets.
Use it when the user asks to hatch or create a pet, mascot, or custom
animated character; make a pet from a brand name or reference image; repair
or upgrade an existing pet atlas; or run the pet QA/packaging pipeline. It
produces the Codex-compatible v2 atlas contract consumed by `apps/pets`:
8 columns x 11 rows of `192x208` cells (1536x2288), the 9 standard animation
rows plus 16 clockwise look directions, packaged with
`spriteVersionNumber: 2`. `apps/pets/internal/char/atlas.go` maps NusaShell
states (`idle`, `thinking`, `reasoning`, `done`, `error`, `waiting`) onto
hatch-pet rows and loads the look rows for idle hover gaze.

When validating an authored pet in `apps/pets`, keep the artwork distinct from
the runtime activity bubble: the bubble is painted by Go, not baked into the
atlas. Its antialiased dark panel uses subtle surface lighting and light text:
a 14px bold header and 12px regular detail, left-aligned without a status dot.
It shows two lines (activity + detail), holds each update for at least
4 seconds, and coalesces fast events to the newest one. Thinking copy rotates
every 5 seconds; it is local status wording, not streamed model reasoning.
`Executing…` shows the tool name as `name(...)`, without arguments. Idle hides
the bubble after the current dwell. To verify bubble composition, a good call
is `exec(command="cd apps/pets && go test -tags sdl2 ./internal/renderer -run TestBubble")`
from the repository workspace. Bad: regenerate `spritesheet.webp` to change
status text, or infer a model's reasoning from the bubble wording.

Read the seeded package progressively:

    skill(op="search", query="hatch pet")
    file_list(path="<datadir>/skills/hatch-pet/scripts")
    file_read(path="<datadir>/skills/hatch-pet/SKILL.md")

The `scripts/` folder contains the deterministic Pillow pipeline (run checks,
frame extraction, atlas composition, validation, despill, QA sheets; invoke
through `exec` with `python3` after confirming Pillow is installed). The
`references/` folder holds the v2 contract, look-direction acceptance policy,
QA rubric, the full command sequence, and the isolated review worker prompts.
Visual generation uses `generate_media`; independent blind and final visual
QA use `subagent`/`delegate` workers. Packages stage to
`<datadir>/pets/<pet-id>/` with `pet.json` + `spritesheet.webp`.
