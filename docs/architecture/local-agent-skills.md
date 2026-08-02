# Local agent skills

NusaShell maintains a local, managed skills library for portable instruction
packages. This is deliberately smaller than the full platform described in
`agent-skills-platform-technical-spec.md`.

## Current boundary

- An install accepts a ZIP-compatible `.skill` or `.zip` archive containing one
  package-root `SKILL.md`.
- `SKILL.md` frontmatter must contain a lowercase slug `name` and a
  `description`. The name becomes the stable installed ID. Optional
  `requirements.mcp` lists concrete plugin ids or role tokens such as
  `role:files`; `compatibility` and string `metadata` are preserved.
- Electron stores copied packages below `userData/skills`. Built-in packages are
  seeded from `resources/agent/skills/` with `builtin` provenance; editing and
  deletion never mutate the source package.
- The application layer owns `SkillRegistryPort`; filesystem and archive work
  stays in the infrastructure adapter.
- Archive extraction rejects absolute/traversal paths and symbolic links, and
  caps entry count, per-file size, and expanded package size. Registry reads and
  writes resolve only below the selected managed skill.

## Agent tools

The shell exposes read-only meta-tools and a gated mutation meta-tool on every
agent turn:

- `skill_list` returns bounded installed-skill summaries.
- `skill_search` searches names and descriptions.
- `skill_read` reads `SKILL.md` or another bounded text file using a skill ID
  and relative path.
- `skill_manage` lets the agent create, edit, write support files in, or delete
  **agent-owned** skills only. User-installed skills are protected and cannot be
  mutated by the model.

### Provenance

A `SkillProvenancePort` sidecar (`.provenance.json` in the skills root) tracks
whether each skill was created by the agent or installed by the user.

- `installFromArchive` marks the skill as `user` origin.
- Built-in seed packages are marked `builtin` and are protected from
  `skill_manage` mutation/deletion.
- `skill_manage` `create` marks the skill as `agent` origin.
- `skill_manage` `edit`, `write_file`, and `delete` check provenance before
  mutating; non-agent skills return a `skill_protected` error.

### SKILL.md validation

- The `description` frontmatter field must be **1024 characters or fewer** and
  should explain what the skill does and when to use it with trigger terms.
- The `name` frontmatter field must match the skill ID slug.
- Support file creation via `write_file` is limited to `references/`,
  `templates/`, `scripts/`, and `assets/` subdirectories.

### Write-approval staging

When `skills.write_approval` is enabled in the desktop config, skill mutations
from `skill_manage` are staged as pending writes (`.pending/{id}.json` in the
skills root) instead of applied immediately. The desktop UI shows pending
writes with Approve and Reject buttons. Approving applies the mutation;
rejecting discards it.

Skill content is untrusted context. Installation, editing, and deletion are
also available as desktop UI operations.

`skill_exec` is intentionally absent. Adding it requires a separate decision
covering process isolation, interpreter policy, filesystem/network access,
resource limits, user approval, cancellation, and audit logging.

## Background Learning Review

After each successful agent turn, a `BackgroundReviewScheduler` ticks counters
and fire-and-forget spawns a restricted review turn when thresholds are
crossed. The review turn uses a `ReviewAgentToolGateway` that whitelists only
`memory`, `skill_list`, `skill_search`, `skill_read`, and `skill_manage` —
no MCP/plugin tools are available.

### Settings

| Setting | Default | Description |
| --- | --- | --- |
| `enabled` | `true` | Master toggle |
| `memoryEveryNTurns` | `10` | Turns between memory reviews |
| `skillEveryNToolRounds` | `10` | Tool rounds between skill reviews |
| `maxToolRounds` | `6` | Max rounds for the review turn |
| `transcriptTailMessages` | `40` | Messages from the end of the transcript to send |

### Write origin and staging

When `writeOrigin` is `"background_review"`, skill mutations are staged via
`SkillApprovalStaging` instead of applied directly. The user sees pending
writes in the desktop UI and can approve or reject them.

### Event

When the review turn produces mutations, an `agent.learning_updated` event is
dispatched through the `EventDispatcher` and mapped to a WebSocket event. The
desktop launcher shows a toast notification.

### State persistence

Review counters are stored in `{memoryRoot}/.review-state.json` using
`FilesystemReviewStateStore`. The file is atomically written and survives
restarts.

### Skill curator (growth control)

Agent-owned skills are automatically curated via the `SkillCuratorService` and
`SkillCuratorScheduler`. See [skill-curator.md](./skill-curator.md) for the
full lifecycle, usage sidecar, eligibility rules, and scheduler configuration.
