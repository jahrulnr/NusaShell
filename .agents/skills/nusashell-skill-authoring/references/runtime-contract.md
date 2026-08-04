# NusaShell runtime skill contract

## Source and seeding

The repository source for builtin runtime skills is:

```text
resources/agent/skills/<skill-id>/SKILL.md
```

At backend startup, `seedBuiltinSkills()` scans folders with a valid lowercase
slug and `SKILL.md`, copies them into the configured managed skills root, and
records `createdBy: builtin` in `.provenance.json`. A destination with non-builtin
provenance is left untouched. This is why a coding agent edits the repository
source, not a user's application-data directory.

## Package shape and limits

Minimum package:

```text
<skill-id>/
└── SKILL.md
```

Optional support files may live only below `references/`, `templates/`,
`scripts/`, or `assets/`. The registry requires the frontmatter `name` to match
the installed id and limits the description to 1024 characters. Archive
installation accepts `.skill` and `.zip`, rejects unsafe paths/symlinks, and
enforces entry, file, and expanded-size limits.

The registry provides `list`, `search`, `get`, `read`, `create`, `write`,
`delete`, `archive`, and `restore`. Reads are bounded and can return an offset
for progressive loading. `SKILL.md` and existing UTF-8 support files are
editable only within the registry's size limits.

## Tool and provenance behavior

The shell exposes `skill_list`, `skill_search`, and `skill_read` for discovery
and context loading. `skill_manage` handles agent-owned mutation; create/edit
may be approval-staged when the runtime enables that policy. Jobs/background
runs are not allowed to mutate skills.

`requirements.mcp` is metadata for a soft prompt gate. It does not enforce a
permission boundary and does not replace live `mcp_list`, `tool_list`, or
`tool_schema` discovery. There is no `skill_exec` tool.

## Validation

For a repository source skill, run the skill-structure validator when available,
then run focused tests:

```bash
pnpm exec vitest run apps/backend/tests/builtin-skill-seed.test.ts
pnpm exec vitest run packages/infrastructure/tests/filesystem-skill-registry.test.ts packages/infrastructure/tests/filesystem-skill-registry-archive.test.ts
```

If the skill is added to `resources/agent/skills/`, confirm the seed test still
recognizes the folder and that the runtime skill list can parse its frontmatter.
