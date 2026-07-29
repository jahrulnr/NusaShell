# Local agent skills

NusaShell maintains a local, managed skills library for portable instruction
packages. This is deliberately smaller than the full platform described in
`agent-skills-platform-technical-spec.md`.

## Current boundary

- An install accepts a ZIP-compatible `.skill` or `.zip` archive containing one
  package-root `SKILL.md`.
- `SKILL.md` frontmatter must contain a lowercase slug `name` and a
  `description`. The name becomes the stable installed ID.
- Electron stores copied packages below `userData/skills`. Editing and deletion
  never mutate the source archive.
- The application layer owns `SkillRegistryPort`; filesystem and archive work
  stays in the infrastructure adapter.
- Archive extraction rejects absolute/traversal paths and symbolic links, and
  caps entry count, per-file size, and expanded package size. Registry reads and
  writes resolve only below the selected managed skill.

## Agent tools

The shell exposes three read-only meta-tools on every agent turn:

- `skill_list` returns bounded installed-skill summaries.
- `skill_search` searches names and descriptions.
- `skill_read` reads `SKILL.md` or another bounded text file using a skill ID
  and relative path.

Skill content is untrusted context. Installation, editing, and deletion are
desktop UI operations and are not exposed to the model.

`skill_exec` is intentionally absent. Adding it requires a separate decision
covering process isolation, interpreter policy, filesystem/network access,
resource limits, user approval, cancellation, and audit logging.
