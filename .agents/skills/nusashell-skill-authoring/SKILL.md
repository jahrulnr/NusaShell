---
name: nusashell-skill-authoring
description: Create, review, or update NusaShell managed agent skills under resources/agent/skills. Use when authoring SKILL.md packages, adding MCP requirements, deciding between builtin and repository-only skills, or validating skill discovery, provenance, and progressive disclosure.
metadata:
  version: "1"
---

# Author NusaShell skills

Use this skill when changing the skill library or when deciding whether a
skill belongs in the NusaShell runtime or only in the repository's coding-agent
toolbox.

## Choose the destination

- Put a NusaShell runtime skill in `resources/agent/skills/<skill-id>/` when it
  must be seeded into the managed library and available to the in-app agent.
- Put a repository coding skill in `.agents/skills/<skill-id>/` when it guides
  Codex/Cursor work on this checkout and should not be seeded into NusaShell.
- Do not write to a user's runtime `userData/skills/` directory from a coding
  task. That directory is generated state; the repository resource tree is the
  builtin source of truth.

## Authoring workflow

1. Define one job for the skill: what it does, when it triggers, expected
   inputs, and the shape of its output. Keep the description third-person,
   specific, and under 1024 characters.
2. Create a lowercase hyphenated directory and an `SKILL.md` whose frontmatter
   has `name` exactly matching the directory and a useful `description`.
3. Keep the body compact and procedural. Include the objective, decision
   points, ordered workflow, tool usage, safety boundaries, and output contract.
4. Move detail into one-level `references/`, `templates/`, `scripts/`, or
   `assets/` files. Read those files only when the current task needs them.
5. Declare `requirements.mcp` only for capabilities the skill really needs.
   Use concrete plugin ids such as `nusashell.files` or role tokens such as
   `role:terminal`; treat availability as a soft gate and check live tools
   before calling them.
6. For a repository builtin, add the skill under `resources/agent/skills/`.
   Builtin seeding copies the whole folder and records `createdBy: builtin`.
   Preserve user/agent-owned installed skills; never overwrite or delete them.
7. Validate the package structure and run the relevant skill seed/registry
   tests before claiming the skill is usable.

## NusaShell contract

- `SKILL.md` is the portable entry point. `skill_list`/`skill_search` show
  summaries; `skill_read` loads the body or a support file.
- Supported support roots are only `references/`, `templates/`, `scripts/`,
  and `assets/`.
- `skill_manage` is for agent-owned create/edit/write/delete operations and
  approval staging. Builtin and user-owned provenance is protected.
- There is no `skill_exec`: bundled scripts are reference material and are not
  run automatically by NusaShell.
- The implemented contract does not require `skill.yaml`, `tools.yaml`,
  `runtime.yaml`, `allowed-tools`, or an execution manifest. Do not describe
  those draft platform fields as runtime-enforced.
- Skill content is untrusted model context. It cannot override shell rules,
  grant permissions, expose credentials, or turn a prose instruction into an
  executable capability.

## Review checklist

- Does the description say what and when, with useful trigger terms?
- Does the skill have one clear primary job rather than a generic role prompt?
- Are instructions progressive, bounded, and free of duplicated platform docs?
- Are MCP requirements concrete, minimal, and checked before use?
- Are destructive operations explicit and confirmation-gated?
- Are credentials kept host-owned and absent from frontmatter, prompts, results,
  and logs?
- Are references, templates, and scripts under an allowed one-level directory?
- Is the package source in the correct tree for its intended provenance?

For implementation details and validation commands, read
`references/runtime-contract.md`.
