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
| `config/settings.json` | compaction / caching / sound / user prompt / learning-job model (`review_model`) / periodic learner interval (`learner_nudge_interval`) | JSON |
| `config/providers.json` | provider configs (no keys) | JSON |
| `config/acp-agents.json` | ACP subagent configs (command/args/env; env values stored locally, keys only on the wire) | JSON |
| `config/mcp-servers.json` | manual MCP server registry | JSON |
| `conversations/*.json` | one file per agent conversation, including job transcripts. Each one carries a `type`: `conversation` (an interactive Agent room), `background` (a learning job's LLM run), or `automation` (a pipeline `agent:` step). Only `conversation` appears in `agent.conversations.list` and the Agent Rooms pane; the other two stay addressable by id through `agent.conversations.get` and the automation steer operation. Records written before `type` existed carry `Origin: pipeline`, which reads as `automation`. On load, a leftover `status: running` (process crash mid-turn) is converted to idle and in-flight assistant messages are marked interrupted. A turn that exits without a terminal state (panic recovered in-process) is also healed immediately: the `runTurn` defer resets the conversation to idle and emits a turn-error event, and `agent.turns.start` heals an orphaned running conversation with no active run before starting a new turn instead of returning 409. | JSON |
| `conversations/*.chunks/` | archived conversation chunks (compaction). Hydration checkpoints (synthetic runtime snapshots) are stripped before archive and summarization; a fresh checkpoint is written in the same compaction Save (`ResetTranscript` then `Add`), including emergency overflow retries. The conversation ID stays the same so todos, attachments, chunks, and the open room stay attached. Text-summary compaction parks its checkpoint immediately after the epoch's first user message (never before it) so OpenAI and Claude see `system → user → hydration`. Codex remote-v2 compaction keeps its opaque server checkpoint and persisted prefix boundary in the conversation JSON, retains a chronological transcript suffix in the active epoch, filters that prefix to real user turns for the provider request, preserves later user/assistant/tool turns after it, and moves only replaced transcript items into this chunk directory. | JSON |
| `conversations/todos.json` | per-conversation TODO checklists + planning briefs. Each non-empty brief is also mirrored to `conversations/<conv_id>/plan.md` (below) so the agent and ACP subagents can `file_read` it. | JSON |
| `conversations/<conv_id>/plan.md` | mirrored todo brief (generated runtime artifact under the data directory, never the user workspace). Deleting it does not lose data: the JSON store is the source of truth and the next brief update rewrites it. The `todo` result returns this path as `plan_path`. | Markdown |
| `conversations/artifacts.json` | per-conversation interactive artifacts (HTML/CSS/JS) | JSON |
| `skills/<id>/` | skill package: root `SKILL.md` (active checkout), `meta.json` (status/version/origin), `versions/<n>/` snapshots | markdown + JSON |
| `skills/skills.json` | usage cache; status/version live in each skill's `meta.json` | JSON |
| `skills/.provenance.json` | skill authorship log (createdBy, createdAt) | JSON |
| `skills/.deleted-builtin.json` | builtin skills the user deleted (so they are not re-seeded) | JSON |
| `plugins/<id>/` | plugins (manual MCP servers and installed plugins): `manifest.json` + optional `ui/` + optional `skills/` (mounted read-only as `plugin:<id>` skills) | JSON + files |
| `memory/user.md` | always-injected user profile (~4k token cap; first boot copies `resources/templates/user.md` when missing; agents write via `file_*`; Learning → About You) | Markdown |
| `memory/soul.md` | always-injected agent document (~1k token cap; first boot copies `resources/templates/soul.md` when missing; agents write via `file_*`; Learning → About Agent) | Markdown |
| `growth/experiences.jsonl` | experience events recorded at finishTurn; unreferenced entries are retained 180 days (active-job sources are preserved) | JSONL |
| `growth/memories.jsonl` | structured MemoryRecord catalog (learner writes; humans retire); live records are capped at 500, retired/superseded audit rows are retained 30 days | JSONL |
| `growth/jobs.jsonl` | learning jobs (`learner`, plus legacy `consolidate` / `evolve_skill` / `evaluate` / `retire_stale`). Finished jobs carry `llm_conversation_id`; terminal rows and their background transcripts are retained 90 days | JSONL |
| `growth/operations.jsonl` | typed learning-operation audit; terminal operations are retained 90 days | JSONL |
| `memory_project/{key}/` | per-workspace project memory (`index.md`, `guardrails.md`, … plus `archive/` and `scripts/`). Default base; override with Settings → Project memory (`project_memory_base`, for example `~/.memory`) | Markdown |
| `learning/edges.jsonl` | learning edges: content/embedding and metadata `related` links, plus `used_with` links for nodes observed together in one agent turn; stale endpoints are pruned during graph rebuild | JSONL |
| `learning/embeddings.jsonl` | embedding cache for memory/skill entries | JSONL |
| `learning/trajectory.jsonl` | learning trajectory log (one event per line), read by the UI in cursor-based 50-event pages and retained 90 days. Job entries carry `job_id`, `status`, the applied `mutations`, and `llm_conversation_id` | JSONL |
| `learning/provider_params.json` | auto-learned provider/model params (context caps, disabled modalities, and request-shape constraints from 400 errors) | JSON |
| `learning/model_overrides.json` | manual model-metadata overrides (win over catalog + learned) | JSON |
| `attachments/<conv_id>/` | user image/file attachments and generated images (`gen-<toolCallID>.<ext>`) | files |
| `models/tts/<voice>.onnx(.json)` | offline TTS voice models (piper). Installed by the one-click Settings installer or placed manually; `PIPER_VOICES_DIR` overrides this location | binary + JSON |
| `piper/<goos>-<goarch>/` | managed piper engine installed by the one-click Settings installer (binary, `espeak-ng-data/`, shared libs); `PIPER_BIN`/PATH binaries still take precedence at runtime | files |
| `docs/` | optional user-supplied docs that extend the embedded corpus | markdown |
| `logs.jsonl` | activity log (bounded ring) | JSONL |
| `conversations/<conv_id>.acp/` | terminal ACP subagent and internal delegate run snapshots (one JSON file per run, linked to the parent conversation); legacy global `acp_runs.jsonl` migrates here automatically on first use | JSON |
| `credentials.db` | API keys per provider | SQLite |
| `automation/workflows.db` | workflows, runs, schedules, events, waits, locks | SQLite |
| `automation/pipelines/` | pipeline definition YAML files (one per workflow, auto-discovered on boot) | YAML |
| `automation/runs/` | local executor scratch directories | files |
| platform temp `/nusashell/` | runtime scratch only: oversized tool results spilled when in-band output exceeds the tool cap (`exec` 20k head+tail sample; `grep` / `web_fetch` / `web_search` / `docs` ~32KiB prefix), plus TTS/STT/plugin installer staging and whisper work dirs. Path is `filepath.Join(os.TempDir(), "nusashell")` — `%TEMP%\nusashell` on Windows. Callers go through `infrastructure/nusatemp` — never dump files at the temp root. A background sweeper deletes files and directories aged 24h+; OS reboot may empty the parent temp dir earlier. | files |

Credentials never appear in the JSON/JSONL files. Deleting the data
directory removes everything, including stored keys.

Deleting a conversation via `agent.conversations.delete` cascades to its
data-dir sidecars: `conversations/<id>.json`, the archived
`conversations/<id>.chunks/` directory, the `conversations/<id>.acp/`
subagent run snapshots, the `conversations/<id>/` plan directory, and
`attachments/<id>/` (image/file attachments + generated images). Live ACP
runs for the conversation are stopped first. Workspace files
(`file_write` / `show` results) live under the user-selected workspace and
are kept; growth experiences, learning jobs, and memory records are also
unaffected by conversation delete.

The optional login service writes its platform definition outside the data
directory: a systemd user unit at `~/.config/systemd/user/nusashell.service`
(Linux), a LaunchAgent at `~/Library/LaunchAgents/id.nusashell.core.plist`
(macOS), and a Scheduled Task named `NusaShell Core` plus a Startup-folder
fallback entry (Windows). Each definition carries only the explicit service
configuration plus the captured `PATH`; it does not copy provider credentials
or the rest of the interactive shell environment. Re-run `nusashell service
install` to refresh PATH after changing shell configuration. `nusashell
service uninstall` removes all of them;
`nusashell service status` reports installed/loaded/running state and flags
definition drift against the current install.

Selected provider keys can be copied into `credentials.db` from an
environment variable with the explicit `nusashell seed-providers`
subcommand (for example `OPENROUTER_API_KEY`); see the Providers page for
the supported variables. The server never reads these variables on its own;
the key is still persisted only in `credentials.db`.
