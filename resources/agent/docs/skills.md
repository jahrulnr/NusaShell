# Agent skills

NusaShell's managed skills library uses a directory with `SKILL.md` and optional
one-level support files under `references/`, `templates/`, `scripts/`, or
`assets/`. Discovery is progressive: `skill_list`/`skill_search` show summaries,
then `skill_read` loads the body or a requested support file.

Built-in `mcp-creator` teaches MCP plugin authoring. Built-in `skill-creator`
teaches how to create or improve agent skills with a clear WHAT+WHEN
description, progressive disclosure, and optional frontmatter such as:

```yaml
requirements:
  mcp:
    - nusashell.files
    - role:terminal
```

When a skill declares `requirements.mcp`, check `mcp_list` and enable a suitable
plugin before following tool-dependent steps. This is a soft prompt gate; the
current shell does not refuse `skill_read` when a requirement is unavailable.

Use `skill_manage` for agent-owned creation and support files. Built-in and
user-installed skills are protected by provenance. There is no `skill_exec`:
NusaShell does not run skill scripts automatically.
