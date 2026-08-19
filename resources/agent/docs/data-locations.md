# Data locations

All application data lives under a single data directory. The default is the
platform config directory plus `nusashell`:

- Linux:   `~/.config/nusashell`
- macOS:   `~/Library/Application Support/nusashell`
- Windows: `%APPDATA%\nusashell`

Override with the `NUSASHELL_DATA_DIR` environment variable.

## Layout

| Path | Content | Format |
| --- | --- | --- |
| `conversations/*.json` | one file per agent conversation | JSON |
| `providers.json` | provider configs (no keys) | JSON |
| `acp-agents.json` | ACP subagent configs (command/args/env; env values stored locally, keys only on the wire) | JSON |
| `agent/skills/<id>/` | one directory per user/builtin skill: `SKILL.md` + optional support files | markdown + files |
| `agent/skills/skills.json` | skill metadata (category, state, origin, owned_by, pinned, usage) | JSON |
| `agent/skills/.provenance.json` | skill authorship log (createdBy, createdAt) | JSON |
| `agent/skills/.deleted-builtin.json` | builtin skills the user deleted (so they are not re-seeded) | JSON |
| `plugins/<id>/` | plugins (manual MCP servers and installed plugins): `manifest.json` + optional `ui/` + optional `skills/` (mounted read-only as `plugin:<id>` skills) | JSON + files |
| `MEMORY.md` | primary memory (always-injected working set, ~1k token cap; auto-created on first run) | Markdown |
| `memories/fragments/*.md` | memory fragments (unlimited searchable archive; one markdown file per entry with YAML frontmatter) | Markdown + YAML |
| `learning_reviews/` | background review agent transcripts (one JSON file per review run; viewable from the Learning log) | JSON |
| `logs.jsonl` | activity log (bounded ring) | JSONL |
| `acp_runs.jsonl` | completed ACP subagent run transcripts (one JSON line per run) | JSONL |
| `settings.json` | compaction / caching / sound notification / user prompt settings | JSON |
| `credentials.db` | API keys per provider | SQLite |
| `automation.db` | workflows, runs, schedules, events, waits, locks | SQLite |
| `ci/runs/` | local executor scratch directories | files |

Workspace pipelines live in the project tree as `.nusashell/pipeline.yaml`, not under the data directory.

Credentials never appear in the JSON/JSONL files. Deleting the data
directory removes everything, including stored keys.

Selected provider keys can be copied into `credentials.db` from an
environment variable with the explicit `nusashell seed-providers`
subcommand (for example `OPENROUTER_API_KEY`); see the Providers page for
the supported variables. The server never reads these variables on its own;
the key is still persisted only in `credentials.db`.
