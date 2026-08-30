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
| `config/settings.json` | compaction / caching / sound notification / user prompt settings | JSON |
| `config/providers.json` | provider configs (no keys) | JSON |
| `config/acp-agents.json` | ACP subagent configs (command/args/env; env values stored locally, keys only on the wire) | JSON |
| `config/mcp-servers.json` | manual MCP server registry | JSON |
| `config/skills.json` | legacy skill metadata catalog (skillfs uses `skills/skills.json`) | JSON |
| `conversations/*.json` | one file per agent conversation, including pipeline `agent:` step transcripts (`Origin: pipeline`). Pipeline conversations are addressable by id (`ci_steer`, `agent.conversations.get`) but are omitted from `agent.conversations.list` and the Agent Rooms pane. On load, a leftover `status: running` (process crash mid-turn) is converted to idle and in-flight assistant messages are marked interrupted. A turn that exits without a terminal state (panic recovered in-process) is also healed immediately: the `runTurn` defer resets the conversation to idle and emits a turn-error event, and `agent.turns.start` heals an orphaned running conversation with no active run before starting a new turn instead of returning 409. | JSON |
| `conversations/*.chunks/` | archived conversation chunks (compaction). Hydration checkpoints (synthetic runtime snapshots) are stripped before archive and summarization; a fresh checkpoint is injected on the next provider round after compaction, including emergency overflow retries. The checkpoint is always parked immediately after the epoch's first user message (never before it) so OpenAI and Claude see `system → user → hydration`. | JSON |
| `conversations/todos.json` | per-conversation TODO checklists + planning briefs. Each non-empty brief is also mirrored to a markdown plan file so the agent and ACP subagents can `file_read` it: `<workspace>/.nusashell/plans/<conv_id>.plan.md` when the conversation has a workspace, otherwise `conversations/<conv_id>/plan.md` (below). The mirror is a generated runtime artifact — safe to gitignore in user projects (`.nusashell/plans/`); deleting it does not lose data (the JSON store is the source of truth and the next brief update rewrites it). | JSON |
| `conversations/artifacts.json` | per-conversation interactive artifacts (HTML/CSS/JS) | JSON |
| `skills/<id>/` | one directory per user/builtin skill: `SKILL.md` + optional support files | markdown + files |
| `skills/skills.json` | skill metadata (category, state, origin, owned_by, pinned, usage) | JSON |
| `skills/.provenance.json` | skill authorship log (createdBy, createdAt) | JSON |
| `skills/.deleted-builtin.json` | builtin skills the user deleted (so they are not re-seeded) | JSON |
| `plugins/<id>/` | plugins (manual MCP servers and installed plugins): `manifest.json` + optional `ui/` + optional `skills/` (mounted read-only as `plugin:<id>` skills) | JSON + files |
| `memory/primary.md` | primary memory (always-injected working set, ~1k token cap; auto-created on first run) | Markdown |
| `memory/fragments/*.md` | memory fragments (unlimited searchable archive; one markdown file per entry with YAML frontmatter) | Markdown + YAML |
| `memory_project/{key}/` | per-workspace project memory (`index.md`, `guardrails.md`, … plus `archive/`). Default base; override with Settings → Project memory (`project_memory_base`, for example `~/.memory`) | Markdown |
| `memory/legacy.jsonl` | legacy memory entries (pre-fragment system) | JSONL |
| `learning/edges.jsonl` | learning edges: content/embedding and fragment-metadata `related` links, plus `used_with` links for nodes observed together in one agent turn; stale endpoints are pruned during graph rebuild | JSONL |
| `learning/embeddings.jsonl` | embedding cache for memory/skill entries | JSONL |
| `learning/trajectory.jsonl` | learning trajectory log (one event per line) | JSONL |
| `learning/turns.json` | turn counters for review agent scheduling | JSON |
| `learning/provider_params.json` | auto-learned provider/model params (context caps, disabled modalities from 400 errors) | JSON |
| `learning/model_overrides.json` | manual model-metadata overrides (review agent corrections; win over catalog + learned) | JSON |
| `learning/reviews/` | background review agent transcripts (one JSON file per review run; viewable from the Learning log) | JSON |
| `attachments/<conv_id>/` | user image/file attachments and generated images (`gen-<toolCallID>.<ext>`) | files |
| `models/tts/<voice>.onnx(.json)` | offline TTS voice models (piper). Installed by the one-click Settings installer or placed manually; `PIPER_VOICES_DIR` overrides this location | binary + JSON |
| `piper/<goos>-<goarch>/` | managed piper engine installed by the one-click Settings installer (binary, `espeak-ng-data/`, shared libs); `PIPER_BIN`/PATH binaries still take precedence at runtime | files |
| `docs/` | optional user-supplied docs that extend the embedded corpus | markdown |
| `logs.jsonl` | activity log (bounded ring) | JSONL |
| `conversations/<conv_id>.acp/` | terminal ACP subagent transcripts (one JSON file per run, written by `subagent_wait` or the completion callback and linked to the parent conversation); legacy global `acp_runs.jsonl` migrates here automatically on first use | JSON |
| `conversations/<conv_id>.journal/` | workspace change journal sidecar: `journal.jsonl` (live append-only event log), `journal.jsonl.gz` (archived gzip members, appended at each turn end), and `blobs/` (content-addressed pre-image objects). Records every file change the agent makes (declared, opaque exec, unobserved MCP) so the post-compaction hydration slot can show exactly what the session changed. Deleted together with the conversation. | JSONL + gzip + files |
| `credentials.db` | API keys per provider | SQLite |
| `ci/automation.db` | workflows, runs, schedules, events, waits, locks | SQLite |
| `ci/pipelines/` | pipeline definition YAML files (one per workflow, auto-discovered on boot) | YAML |
| `ci/runs/` | local executor scratch directories | files |
| platform temp `/nusashell/` | runtime scratch only: oversized tool results (`grep`, `exec`, `web_fetch`, `web_search`, `docs`) spilled when in-band output exceeds ~32KiB, plus TTS/STT/plugin installer staging and whisper work dirs. Path is `filepath.Join(os.TempDir(), "nusashell")` — `%TEMP%\nusashell` on Windows. Callers go through `infrastructure/nusatemp` — never dump files at the temp root. A background sweeper deletes files and directories aged 24h+; OS reboot may empty the parent temp dir earlier. | files |

Credentials never appear in the JSON/JSONL files. Deleting the data
directory removes everything, including stored keys.

Selected provider keys can be copied into `credentials.db` from an
environment variable with the explicit `nusashell seed-providers`
subcommand (for example `OPENROUTER_API_KEY`); see the Providers page for
the supported variables. The server never reads these variables on its own;
the key is still persisted only in `credentials.db`.
