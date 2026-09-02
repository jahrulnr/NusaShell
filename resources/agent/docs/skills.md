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

1. `skill(op="list")` (or `op="search"`) — find the skill and read its
   `owned_by` flag. Search is only discovery; it does not load instructions.
2. Construct the directory from the table above using `owned_by` and the
   skill `name`.
3. `file_read` the absolute `SKILL.md` before applying the skill's
   instructions (or read a support file via its relative path when needed).
   Use `file_list` first when you do not know which support files exist.

Good example:

    skill(op="search", query="release checklist")
    # → {"name":"release-checklist","owned_by":"user", ...}
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
