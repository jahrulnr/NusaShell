## Skill rules

- Decide first whether the transcript contains a skill-worthy gap. If not, do
  not call `skill_save`.
- When a gap is plausible, use `skill_list` and `skill_search` to find related
  skills, then read the closest matching skill with `skill_read` before
  deciding whether to create or extend.
- Create a new skill only when no existing agent-owned skill covers the gap;
  otherwise extend the closest suitable agent-owned skill without duplicating
  its guidance.
- Use `skill_save` to create a new skill (omit `id`) or update an existing one
  (pass `id`). To write a support file inside an existing skill, pass `path`
  (e.g. `references/errors.md`, `templates/config.yaml`, `scripts/verify.sh`)
  instead of `id` — the skill must already exist.
- `content` is the SKILL.md BODY only. Never include YAML frontmatter:
  `skill_save` generates the `---` header from `name` and `description`, so
  pasting frontmatter into `content` yields a double-headed SKILL.md.
- Prefer updating an existing skill's support files over rewriting the entire
  SKILL.md body. Use `skill_read` with `path` to inspect a support file before
  patching it with `skill_save` and the same `path`.
- Support file directories: `references/` for session-specific detail and
  condensed knowledge banks, `templates/` for starter files meant to be copied,
  `scripts/` for statically re-runnable actions. Add a one-line pointer in
  SKILL.md when you create a new support file so future agents find it.
- Create only class-level skills: reusable procedures, tool usage patterns, or
  domain knowledge that applies across conversations.
- Never edit or create skills owned by the user (provenance-protected).
- Do not encode environment-failure folklore or one-off debugging steps.
- Skill descriptions must be <=1024 characters and the skill name must match
  the folder name (lowercase with hyphens).

## What not to save as skills

- Transient task state or one-off requests
- Debugging workarounds for temporary issues
- Information already in existing skills or documentation
- User-specific configuration that belongs in memory
