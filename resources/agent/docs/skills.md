# Skills

A skill is a markdown instruction pack the agent can load on demand. Each skill
lives in its own directory `<datadir>/agent/skills/<id>/SKILL.md` plus optional
support files (references, scripts, assets).

## Ownership and priority

Every skill has an `owned_by` field that records who provided it:

- `user` — authored by the user via the Skills workspace or `skill_save`.
- `builtin` — shipped with NusaShell and seeded into the data directory.
- `plugin:<plugin-id>` — bundled inside a plugin's `skills/` directory and
  mounted read-only at plugin install time. Uninstalling the plugin removes
  the skill.

When two owners define a skill with the same ID, the higher-priority owner
shadows the lower one. Priority order: `user` > `builtin` > `plugin:<id>`.
Shadowed skills still appear in `skill_list` (dimmed in the UI) but
`skill_read` and the agent's skill resolution use the winner.

## Authoring

Each skill has a name, a short description and markdown content. The
description is what the agent sees in the tool listing, so make it specific:
what the skill does and when to use it.

Keep instructions imperative and self-contained; the content is injected
verbatim into the agent's context when the skill is loaded.

Plugin-owned skills are read-only — uninstall the plugin to remove or modify
them.

## Using skills

- `skill_list` — enumerate skills (returns `owned_by` and `shadowed` flags).
- `skill_read` — read a skill's `SKILL.md` (or a support file via `path`)
  by name. The agent then follows its instructions for the rest of the turn.
- `skill_files` — list the files inside a skill folder before reading them.
- `skill_save` — create or update a user-owned skill.

The Skills workspace is a read-only browser: it shows the catalog, the file
tree of the selected skill, and a file viewer. Use `skill_save` (or the New
skill button) to author skills.

### skill_save guidance

Save class-level, reusable procedures — never one-off debugging notes or
environment-failure folklore.

Good example:

    skill_save(name="release-checklist",
               description="Steps for tagging a release and verifying the build",
               content="## Release\n1. …")

Bad examples:

    skill_save(name="bug-2026-08-19", description="fix", content="the debug session…")

    skill_save(name="my-api-key-notes", description="credentials", content="sk-…")  # secrets
