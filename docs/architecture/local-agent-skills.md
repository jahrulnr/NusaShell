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

## Skills catalog injection (Layer 1)

Pure "tools only + hope the model lists skills" underperforms: the model rarely
calls `skill_list` spontaneously, so seeded domain skills stay invisible. To
fix this, a budgeted **skills catalog** (name + description for every installed
skill) is injected into the agent's system context every interactive turn,
right after the `mcp-tools` prompt and before the subagent / developer prompts.

- **Source:** `SkillRegistryPort.list()` summaries — the same data that powers
  `skill_list`. Full `SKILL.md` bodies are never injected.
- **Builder:** `buildSkillsCatalogPrompt(summaries, budget)` orders skills with
  priority builtins (`mcp-creator`, `skill-creator`) first, then alphabetical.
  Each description is clamped to ~400 chars; the total block is clamped to
  ~3000 chars. When truncated, a tail note directs the model to `skill_list` /
  `skill_search` for the rest.
- **Injection:** `RunAgentTurnHandler.injectSystemPrompts` calls the builder and
  passes the result to `injectPrompts` as the `skillsCatalogPrompt` parameter.
  The catalog is skipped when there are no skills (no block injected), mirroring
  the `formatMemoryPrompt` empty → undefined contract.
- **Scope:** Interactive turns only. Jobs and background review turns do not
  inject the catalog (jobs denylist skill tools; review turns already have a
  restricted gateway). Subagent turns today have no skill tools → no catalog.
- **Failure mode:** If the registry `list()` call throws, the catalog is
  skipped and a warning is logged — the turn still runs.

The body stays progressive (Layer 2): the model reads a full `SKILL.md` via
`skill_read` only when the task matches a catalog entry. See the Skills workflow
section in `resources/agent/prompts/mcp-tools.md` for the protocol text.

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
