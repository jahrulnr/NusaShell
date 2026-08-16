# Agentskills alignment

NusaShell follows the useful authoring subset of the agentskills.io model:

- package directory + `SKILL.md`;
- required `name` and `description` frontmatter;
- description explains what and when, with trigger terms;
- progressive disclosure from summary to `SKILL.md` to one-level support files;
- optional `compatibility` and `metadata` are preserved.

NusaShell intentionally does not implement the full draft platform: `skill.yaml`,
`allowed-tools` enforcement, and `skill_exec` are out of scope.
