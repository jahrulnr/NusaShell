# Agent skills

NusaShell's managed skills library uses a directory with `SKILL.md` and optional
one-level support files under `references/`, `templates/`, `scripts/`, or
`assets/`. Discovery is **progressive across two layers**:

- **Layer 1 — Catalog (always injected).** Every interactive turn, a budgeted
  catalog of skill **name + description** is injected into the agent's system
  context. The model sees the inventory without calling any tool. Descriptions
  are clamped to ~400 chars; the whole block is clamped to ~3000 chars. When
  truncated, a tail note points to `skill_list` / `skill_search` for the rest.
- **Layer 2 — Body (progressive read).** `skill_list` / `skill_search` show
  summaries; `skill_read` loads the full `SKILL.md` or a requested support
  file. Full bodies are never injected as static system context — they are
  loaded on demand via `skill_read`.

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

## Why a catalog injection?

Pure "tools only + hope the model lists skills" underperforms: the model rarely
calls `skill_list` spontaneously, so seeded domain skills stay invisible. Pure
"paste every `SKILL.md` into system" wastes tokens and cross-contaminates
unrelated skills. The catalog-injection hybrid (Codex / goclaw pattern) keeps
the inventory visible every turn at ~2% of the window, while bodies stay
progressive and cheap.
