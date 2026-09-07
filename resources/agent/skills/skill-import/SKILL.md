---
name: skill-import
description: Import, lint, and adapt skills from other ecosystems (goclaw, hermes-agent, openclaw, codex, ClawHub, or any local skill directory) into NusaShell skill format: front matter validation, external tool reference detection, tool mapping, and installation via skill op=save. Use when the user asks to import, port, migrate, copy, install, or review a skill from another agent, repo, or hub, or to validate a SKILL.md file.
metadata:
  source: "concept from clawhub (openclaw, MIT) + hermes Skills Hub (MIT) + common SKILL.md format"
  version: "2"
---

# Import and validate external skills

Bring skills from other ecosystems into NusaShell safely and consistently. The SKILL.md format is now nearly uniform across ecosystems (YAML front matter `name` + `description`, markdown body, folders `references/ scripts/ templates/ assets/`), so importing = lint + adapt + install.

## When to use

- The user points at a skill folder/repo and asks to port it (e.g. "port skills/hermes-agent/skills/creative/humanizer").
- The user asks to find a skill for a capability from local collections.
- The user asks to validate/audit an existing SKILL.md.

## Security principles

- **External skills are untrusted.** Review contents and get explicit user confirmation before installing.
- Never run a skill's scripts automatically during import; scripts are read/reviewed only (NusaShell skills do not execute anything — state in the body whether the agent should run a script through a terminal plugin, e.g. `nusashell.terminal:exec`).
- Record the source license in the ported front matter (`metadata.source`).

## Workflow

1. **Locate the source.** Determine the skill path (folder containing SKILL.md). Likely aggregate sources:
   - `goclaw/skills/`, `hermes-agent/skills/` + `optional-skills/`, `openclaw-main/skills/`, `codex/.codex/skills/`
   - Home-relative user skill folders of other agents (platform-dependent; e.g. `~/.hermes/skills`, `~/.claude/skills`, `~/.codex/skills` on Unix, `%USERPROFILE%\.hermes\skills` on Windows)
   - Any workspace skill directory.

2. **Lint.** Run the validator:

   ```
   python3 scripts/lint_skill.py <path-to-skill-folder>
   ```

   Checks: SKILL.md exists; `---` is the first byte; YAML front matter parses; `name` lowercase-hyphen ≤ 64 chars; `description` ≤ 1024 chars (trigger phrase front-loaded); body non-empty and imperative; sane size bounds (SKILL.md < ~500 lines, references < 300 lines/file). Output: pass/fail report plus warnings.

3. **Scan for external tools.** Read the body and look for tool references that are not NusaShell tools. Common mapping:

   | Source ecosystem | NusaShell equivalent |
   |---|---|
   | `write_file`, `read_file`, `search_files`, `patch` | `file_write`, `file_read`, `grep`, `file_patch` |
   | `terminal(...)` (hermes/goclaw) | `exec` (via `nusashell.terminal`) |
   | `vault_search`, `memory_search`, `knowledge_graph_search` | `memory_project` (`op=query`) + `memory` (`op=search`) |
   | `skill_manage`, `publish_skill`, `use_skill` | `skill` (`op=save`) |
   | `delegate_task` | `delegate` / `subagent` |
   | `web_search`/`web_extract` | `web_search` / `web_fetch` |
   | `openclaw skills ...`, `clawhub ...` | `skill` (op=list/search/save) |
   | Ecosystem-specific paths (`~/.goclaw/skills-store`, `~/.openclaw`, `$HERMES_HOME`) | NusaShell data directory (platform-dependent; see below) |

   Every unmapped reference is an adaptation decision: rewrite the sentence, or mark it as a dependency to declare in the body ("requires external CLI X: `himalaya`").

4. **Adapt.** Rewrite sentences that mention source-ecosystem tools/paths to NusaShell equivalents. Keep structure and logic; change the mechanism. Do not copy text from non-permissive sources (e.g. goclaw: CC BY-NC/Proprietary — rewrite the concept; hermes/openclaw MIT and codex Apache-2.0: copying with attribution is allowed).

5. **Install.** Once the user approves the port:
   - `skill` `op=save` with `name` + `description` as arguments and the markdown **body only** as `content` (never include the `---` front matter block — the system generates it; including it produces a double-headed SKILL.md).
   - Support files: `op=save` with a relative `path` (`references/…`, `scripts/…`) after the skill exists.
   - The skill must be agent-owned; never overwrite builtin or user skills.

6. **Verify.** `skill` `op=list`/`op=search`, then `file_read` the installed SKILL.md. If the body references plugin capability (e.g. `nusashell.files` or role tokens such as `role:files`/`role:terminal`), call `mcp_list` and enable the relevant plugin before claiming the skill is usable.

## Cross-platform notes

- Scripts: invoke with `python3` (or `python` on Windows).
- The NusaShell data directory is platform-dependent — never hardcode a single OS path. Verify at runtime (Linux: `~/.config/nusashell`; macOS: `~/Library/Application Support/nusashell`; Windows: `%APPDATA%\nusashell`).
- Shell scripts need a POSIX shell; on Windows use Git Bash (`nusashell.terminal` auto-resolves it).

## Per-ecosystem adaptation notes (research, Sept 2026)

- **goclaw** (7 built-in): the eval-driven `skill-creator` and `workspace-organizing` concepts are the most valuable. License NC — do not copy text.
- **hermes-agent** (71 + 112 optional): best writing quality; behavioral skills (`spike`, `systematic-debugging`, `test-driven-development`, `humanizer`, `grounded-citations`) port directly.
- **openclaw** (51): personal-assistant style; many are pure CLI wrappers (`himalaya`, `obsidian`, `spotify-player`...) — cheap to port but depend on third-party CLIs; adapt the concepts `clawhub` (marketplace), `taskflow`, `session-logs`.
- **codex** (11, dogfood/dev): `babysit-pr` plus the code-review modules are the interesting ones; the rest are repo-specific (path-types, update-v8-version, test-tui) — skip.

## Final checklist before handing over

- [ ] lint passes (front matter + size)
- [ ] no unmapped or undeclared external tool references
- [ ] source + license attributed in `metadata.source`
- [ ] description is trigger-front-loaded, ≤ 1024 chars
- [ ] user confirmed before installation