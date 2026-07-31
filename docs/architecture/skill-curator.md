# Skill Curator — Growth Control

The skill curator protects the agent's skill library from unbounded growth by
tracking usage and auto-archiving unused agent-owned skills.

## Lifecycle

Agent-owned skills follow an `active → stale → archived` lifecycle:

1. **active** — the default state for newly created or recently used skills.
2. **stale** — no activity for `staleAfterDays` (default: 30). The skill remains
   visible but is marked stale.
3. **archived** — no activity for `archiveAfterDays` (default: 90). The skill
   directory is moved to `.archive/` under the skills root and is no longer
   listed by `skill_list` or `skill_search`.

Restoring an archived skill moves it back to the active skills root and resets
its state to `active`.

## Usage sidecar

A `.usage.json` file in the skills root tracks per-skill metrics:

| Field | Description |
| --- | --- |
| `useCount` / `lastUsedAt` | Incremented when the agent calls `skill_manage`. |
| `viewCount` / `lastViewedAt` | Incremented when the agent calls `skill_read`. |
| `patchCount` / `lastPatchedAt` | Incremented on skill mutations (create, edit, write_file). |
| `state` | Current lifecycle state (`active`, `stale`, `archived`). |
| `pinned` | If `true`, the curator skips this skill entirely. |
| `archivedAt` | Timestamp when the skill was moved to `.archive/`. |

Usage bumps are fire-and-forget and never throw on the tool path.

## Eligibility rules

- **Pinned skills are always skipped** — the curator never transitions or
  archives a pinned skill. The agent also cannot delete a pinned skill
  (`skill_manage` `delete` returns `skill_pinned`).
- **User-owned skills are skipped** unless `pruneUserOwned` is `true` (default:
  `false`). Only agent-owned skills are curated in this ship.
- **Never-used grace** — a skill with zero activity uses its `createdAt` as the
  baseline, so newly created skills are not immediately stale.

## Scheduler

A `SkillCuratorScheduler` ticks after each agent turn (alongside the background
review scheduler). It respects:

| Setting | Default | Description |
| --- | --- | --- |
| `enabled` | `true` | Master toggle. |
| `intervalHours` | `168` (7 days) | Minimum time between automatic curator runs. |
| `paused` | `false` | When `true`, automatic ticks are blocked. Manual runs still work. |

State is persisted in `.curator-state.json` under the skills root.

### Manual run

The desktop UI can trigger a curator run (dry-run or real) via IPC. Manual runs
bypass the interval gate but still respect the `paused` flag.

## Event

When a curator pass produces changes, an `agent.learning_updated` event is
dispatched with `kinds: ["skill_curator"]`. Dry-runs and no-op passes do not
dispatch events.

## Desktop UI

The skills panel exposes:

- **Curator status** — last run time and whether a run is in flight.
- **Run curator** — trigger a dry-run or real run.
- **Pin / unpin** — prevent a skill from being curated or deleted.
- **Archived list** — view and restore archived skills.
- **Configure** — adjust curator and scheduler settings.
