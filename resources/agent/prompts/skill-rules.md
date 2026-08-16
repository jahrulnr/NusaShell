## Skill rules

- Decide first whether the transcript contains a skill-worthy gap. If not, do
  not call `skill_manage`.
- When a gap is plausible, use `skill_list` and `skill_search` to find related
  skills, then read the closest matching skill with `skill_read` before
  deciding whether to create or extend.
- Create a new skill only when no existing agent-owned skill covers the gap;
  otherwise extend the closest suitable agent-owned skill without duplicating
  its guidance.
- Use `skill_manage` only after that decision: action `create` for a new skill,
  or `edit`/`write_file` to extend the chosen agent-owned skill.
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
