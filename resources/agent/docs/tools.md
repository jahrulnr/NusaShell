# Agent tools

The agent ships with a built-in toolbox plus one tool per MCP server tool.

## Built-ins

| Tool | Purpose |
| --- | --- |
| `exec` | run a shell command as a child process; combined stdout/stderr streamed live to the tool terminal as it is produced (head/tail elision for huge output) plus an optional per-call Stop button while running; default shell is POSIX `sh` on Unix/macOS, on Windows `auto` resolves Git Bash first (POSIX syntax works best) then PowerShell — `cmd` only via explicit `shell="cmd"`, plus optional kinds `bash`/`powershell`/`pwsh`/`wsl` (wsl maps cwd under /mnt); no absolute wall-clock limit — running commands keep producing, but silence longer than `idle_timeout_ms` (default 180000) fails the run; optional explicit `timeout_ms`; optional absolute `cwd`; the whole child tree dies on cancel/timeout (Stop button or composer Stop); on Windows select shells via `shell=` instead of invoking cmd.exe/powershell.exe inside a bash command line (MSYS path conversion mangles drive-letter paths like `Z:/x`). When the full log exceeds ~32KiB, YAML includes `overflow_path` (absolute file under the platform temp dir `nusashell/`) with the complete stdout/stderr — `file_read` it from offset 0 |
| `file_read` | read a text file by absolute path (up to `max_bytes`, default 32768; continue with `offset_bytes` when truncated — the result echoes `offset_bytes`/`next_offset_bytes`, always byte counts, never line numbers; `sha256` is the version of the complete file, including bytes outside the returned page; metadata also reports `line_ending`, `tabs`, `carriage_returns`, and `trailing_whitespace_lines`; use `show_whitespace=true` to render invisible whitespace as visible markers; binary files are reported, not dumped) |
| `file_write` | create or overwrite a text file atomically (temp file + rename); parent directories created automatically; `encoding` is utf8, escaped visible-whitespace text, or base64; escaped text understands `\t`, `\r`, `\n`, and `\\` and preserves the resulting bytes; transient Windows file-lock errors during the rename are retried briefly; the result includes the written file's `sha256` and whitespace metadata |
| `file_patch` | exact substring replace; after an exact miss, safely auto-heals one unique whitespace-equivalent match by default and reports `healed: true`; set `auto_heal=false` for exact-only behavior; repeated exact matches still require 1-based `occurrence`, while ambiguous whitespace matches never write and report the current version plus candidate line numbers (`candidate_lines=[...]`); use `encoding=escaped` when copying markers from `file_read(show_whitespace=true)`; pass `expected_sha256` from `file_read` to fail closed if the file changed; success returns the new `sha256` and whitespace metadata; a no-match context failure returns the current version, whitespace statistics, and a nearby excerpt with invisible characters rendered visibly; `preview=true` returns the result without writing |
| `file_list` | list directory entries with name, type, size, modified time |
| `file_mkdir` | create a directory including any missing parents |
| `file_delete` | delete a file or directory (non-empty directories require `recursive=true`); irreversible |
| `file_move` | move/rename a path; overwrites an existing destination; falls back to copy+delete across filesystems (transient Windows rename locks are retried briefly first) |
| `file_copy` | copy a file or directory recursively |
| `file_info` | metadata for a path: `exists`, size, mode, type, modified time. Does NOT error on missing paths — returns `exists: false` (use it for existence checks too) |
| `grep` | search file contents with regex (RE2 syntax); filters by `glob_pattern`, returns matching lines with optional `context_lines`; `output_mode`: content (default), files_with_matches, count; case-insensitive via `case_insensitive=true`; set `show_whitespace=true` in content mode to render tabs and carriage returns visibly; content rows are `path:LINE:text` where LINE is a 1-based line number (context rows use `path-LINE-text`), and the header tallies them as `line_matches` (when `max_results` caps the result, the header also reports `total_line_matches` from the search totals; count mode: `file:N` = match count, `total_line_matches`); skips `.git`, `node_modules`, `vendor`, and `*.min.js`/`*.min.css`/`*.map`; each content line is clipped at 200 bytes; in-band body caps at ~32KiB with `overflow_path` / `next_offset_bytes` so `file_read` can page the rest from the platform temp dir; prefer this over exec+shell grep — structured output, no process spawn, works without rg installed |
| `find_file` | find files by glob pattern with `**` recursive matching (e.g. `**/*.go`) and brace expansion (e.g. `*.{go,ts}`); skips .git/node_modules/vendor; returns matching paths sorted alphabetically |
| `show` | render a file from disk in the UI. `op=html` reads an HTML file and displays it in a sandboxed iframe (write the file first with `file_write`, then `show` it — use `file_patch` for edits, `file_read` to inspect). `op=image` reads an image file and displays it inline. `op=audio` reads an audio file (mp3, wav, ogg, m4a) and displays an inline player. `op=video` reads a video file (mp4, webm, mov, avi) and displays an inline player. `width`/`height` control the iframe viewport (html only, default 720x400). The tool result to the model is metadata only (path, name, media_type, size_bytes for media; path, width, height, title for html) — no file content or base64 payload is ever embedded in the tool output, so it does not bloat the conversation JSON or enter the provider request. The frontend loads the file via `/local-file?path=` on demand. Use `read_media` instead when the model needs to see the image/audio/video content. Replaces the former `artifact_*` tools — file_* handles CRUD, `show` only handles display |
| `skill` | skill library dispatcher; `op` selects: `list`, `search`, `save`. Skill files live on disk — read `SKILL.md` and support files with `file_read`, list a skill folder with `file_list` (see `docs(op="read", id="skills")` for the path layout) |
| `memory` | long-term memory dispatcher; `op` selects: `save` (idempotent dedup), `replace` (primary substring/body rewrite or fragment update), `search` (BM25 ranked fragments), `list`, `delete` |
| `memory_project` | per-workspace project memory dispatcher (listed only with a workspace); `op` selects: `query` (AND selectors), `list`, `read`, `admit` (upsert + lint), `skip` (negative admission, no write), `archive`, `lint`. User prefs stay in `memory`. See `docs(op="read", id="memory-project")` |
| `docs` | product documentation dispatcher; `op` selects: `search {query}` (ranked page ids) and `read {id}`. Long `read` pages are truncated in-band (~32KiB) with `overflow_path` — continue via `file_read` |
| `todo` | manage the conversation task checklist. Two modes: `replace` (default) full-replaces the list (an empty items array clears it); `patch` merges by ID — updates status/content of existing items, appends new ones, keeps untouched items unchanged (`content` optional in patch mode). Use `patch` to update a single item without re-emitting the full list. Item IDs are shown in the hydrated checklist so statuses can be patched after compaction. Max 50 items, 500 chars each; prefer exactly one `in_progress` at a time. The optional `brief` argument is a living planning document (max ~10k tokens) with required markdown sections `## Objective` and `## Done when`, plus optional `## Findings` and `## Approach` that grow as the task progresses. It stays available through conversation history and is re-injected with the fresh hydration checkpoint immediately after the compacted handover user (the epoch anchor), before retained assistant rounds. The brief is mirrored to a plan file on disk; the result returns `plan_path` (absolute) — `file_read` it to re-read the latest brief, and hand it to ACP subagents. Set `clear_brief: true` to delete the brief and its plan file (items untouched); an empty `brief` string alone never clears. The user can delete items from the UI — treat deleted items as gone and do not re-add them. |
| `ask_question` | block for a structured user decision; use only when progress genuinely requires a choice or approval. This is the only way to pause the todo-driven auto-continue chain for a user answer — a plain-text question in the reply does not pause it. Set `multi_select=true` whenever more than one option could fit (preferences, scope, priorities) so the user can pick several; the user can also add free text as a note/suggestion alongside the chosen options (when `allow_free_text=true`) |
| `mcp_list` | list all plugins (MCP servers) with runtime state: every plugin appears, running or idle |
| `tool_list` | list ALL tools of a running MCP server (no query); accepts plugin id only; returns compact entries (ref, name, server, description) without parameter schemas — load the full schema with `tool_schema` when needed; call after `mcp_enable` to discover tools |
| `tool_schema` | load one MCP tool's full definition as a single JSONL line (name, description, parameters with type/properties/required); accepts plugin id only; this is the only tool that serves schemas — mcp_search and tool_list stay schema-free |
| `mcp_search` | universal MCP tool discovery — search running MCP servers by name or description (token match, ranked); returns compact matches (`ref` `<plugin-id>:<tool>`, name, description) without inlining parameter schemas so large catalogs stay token-cheap; call `tool_schema` for exact argument fields when needed; always prefer `mcp_search` + `mcp_call` over guessing tool names |
| `mcp_call` | the only MCP tool execution path — run a tool by `ref` (from `mcp_search` or `tool_list`; format `<plugin-id>:<tool>`) with `arguments_json` (a JSON-encoded string of the arguments matching the tool's parameters schema, or a JSON object directly; optional — defaults to `{}` for parameterless tools); returns a `STALE_TOOL_REF` error if the server was disabled/restarted since discovery (re-search and retry) |
| `contract_read` | read the usage contract declared by a plugin (`id=<plugin-id>` or `id=all`); marks the contract as read for the current conversation so `mcp_call` can proceed in `require` mode. Advisory by default (`plugin_contract_mode` defaults to `hint`); enforcement is only active when the setting is set to `require`. Call this before using any plugin that declares a `contract.entry` in its manifest |
| `mcp_register` | copy a plugin from an absolute staging folder outside the installed plugins root; check inventory and ask before replacing an existing id |
| `mcp_enable` | connect an installed plugin so its tools become available; returns only status + tool count — follow with `tool_list` or `mcp_search` to discover tools; returns `already_enabled` if already connected (no reconnect) |
| `mcp_disable` | stop a plugin without uninstalling it |
| `mcp_unregister` | permanently delete an installed plugin; ask first and use `mcp_disable` when it only needs to stop |
| `mcp_install` | install a plugin from the curated catalog or GitHub |
| `mcp_server_add` | register a manual MCP server from command, arguments, and environment entries |
| `read_media` | load any media file (image, audio, video, or PDF document) from disk by absolute path into the model's context — the media kind is auto-detected from binary magic bytes, no need to specify image/audio/video/pdf. Any path works, not just conversation attachments. Vision/audio/video-capable models see/hear it directly; document-capable models receive the PDF natively (Anthropic `document`, OpenAI `input_file`); non-capable models get a text description/transcript via the configured fallback (vision fallback, cloud STT + offline whisper for audio, video fallback) or a placeholder note with the file path (document) |
| `generate_media` | generate media from a prompt and save it for the user: media_type=image (PNG/JPEG/WebP; referenced_image_paths enables image-to-image editing), speech (mp3/wav/opus via OpenAI-compatible /audio/speech or offline piper), or video (async /videos API; duration/resolution minimums reported verbatim on rejection; referenced_image_paths enables image-to-video — first image becomes the first frame, additional images are style references). Only listed when at least one mode is configured; unconfigured modes are rejected with guidance. Speech routing: an explicitly picked offline piper voice (Settings → Speech generation, provider "piper", appears once installed via one-click install under `<data>/models/tts/`) wins, then the configured online model, then offline piper as automatic fallback — the fallback is live as soon as the engine is installed, no enable/disable flag involved |
| `web_search` | search the web across Brave, Serper, Tavily, Startpage, Wikipedia, and GitHub (pool configurable in Settings → Web Search); returns ranked results with title, URL, and snippet. Oversized JSONL is truncated in-band (~32KiB) with `overflow_path` |
| `web_fetch` | fetch a URL and return readable text; supports HTML, JSON (pretty-printed), XML/RSS/Atom, Markdown, CSV, and plain text with newlines preserved; collects links and selected response headers; honors `max_bytes` (extract cap, default 2MB); in-band body caps at ~32KiB with `overflow_path` / `next_offset_bytes`; surfaces `Retry-After` on 429/503 and structured JSON error bodies |
| `web_answer` | get a web-grounded answer via an LLM with built-in web search (only available when an answer-provider API key is configured) |
| `ci_run` | start a saved CI workflow by `workflow_id`; set `async: true` to return immediately with a `run_id` while the pipeline runs in the background |
| `ci_wait` | block until a run reaches a terminal state or the timeout expires; use after `ci_run` with `async: true` |
| `ci_run_status` | DAG summary and status; use this after `ci_run` |
| `ci_logs` | job log tail (prefer failed jobs) |
| `ci_cancel` | cancel a run |
| `ci_steer` | send additional instructions to a running pipeline agent step |
| `ci_list` | saved CI workflows with availability (`runnable` / `blocked` / `disabled` / `invalid`); `invalid` stays listed and cannot be enabled or run until the YAML is fixed |
| `ci_read` | one CI workflow plus capability bindings |
| `ci_validate` | validate YAML |
| `ci_create` | persist a once/every/when/manual workflow (NusaShell owns the schedule) |
| `ci_enable` / `ci_disable` | lifecycle without deleting; `ci_enable` rejects `invalid` YAML |
| `ci_status` | inspect waiting/blocked runs |
| `ci_schedule_once` | one-shot RFC3339 CI workflow |
| `ci_schedule_every` | cron or interval CI workflow (not equivalent) |
| `wait_until` | durable wait; the runner is not occupied |
| `sleep` | pause 1–300 seconds; use for retry backoff or between polls of an async `ci_run` |
| `subagent` | spawn 1–6 ACP coding-agent sessions (only listed when at least one ACP agent is enabled in Providers; never listed for pipeline `agent:` steps) |
| `subagent_steer` | queue an extra instruction on a live ACP run |
| `subagent_stop` | cancel a live ACP run (pending permissions fail closed) |
| `subagent_wait` | wait for an async ACP run to finish |
| `delegate` | spawn one internal NusaShell background agent: the same engine, headless, in a hidden pipeline room, with the standard toolbox (no `subagent`/`delegate`, no permission prompts). It does not receive this conversation's history — pass a compact brief with absolute paths. Always async: returns a run id immediately; the tool call stays `running` until the delegate finishes, then a synthetic `delegate_result` tool call is injected at the next steer-style round boundary (or a new turn if idle). Never listed for the delegate agent itself (no recursion) |

## Coordinate discipline (grep ↔ file_read ↔ file_patch)

`grep` and `file_read` speak different coordinate systems, and both are
correct: grep reports **1-based line numbers** (`path:LINE:text`, tallied as
`line_matches`), file_read reports **byte offsets** (`offset_bytes`,
`next_offset_bytes`). Never convert between them with arithmetic — line
lengths vary and CRLF files count one line but two bytes per newline, so
mental math lands hundreds of lines off. Anchor on **content** instead:
numbers are for orientation, content is the only anchor all three tools
share.

```text
# GOOD — grep locates, content anchors the edit
grep(pattern="version=1\\.2\\.3", path="deploy.yaml")
  → deploy.yaml:2:deploy: version=1.2.3 env=prod
file_patch(path="deploy.yaml",
  old_string="deploy: version=1.2.3 env=prod",   # verbatim from the grep row
  new_string="deploy: version=2.0.0 env=prod")

# GOOD — truncated read continues with its own byte coordinate
file_read(path="big.log")            → next_offset_bytes: 32768
file_read(path="big.log", offset_bytes=32768)

# GOOD — oversized grep/web_fetch/docs/web_search: read the spill, don't re-run
grep(...) → overflow_path: /tmp/nusashell/grep-ab12.txt  next_offset_bytes: 32768
file_read(path="/tmp/nusashell/grep-ab12.txt", offset_bytes=32768)

Spill files live under the platform temp dir (`nusashell/`) and are swept
after 24 hours while the server is running.

# BAD — treating a line number as a byte offset
grep(...) → line 900
file_read(path="big.log", offset_bytes=900)   # lands mid-line ~14, not line 900

# BAD — arithmetic between coordinate systems
file_read(...) → next_offset_bytes: 32768
grep(..., "line 32768")                        # bytes ≠ lines
```

### Invisible whitespace discipline

`file_read` preserves bytes and reports whitespace metadata for the complete
file. When tabs or CRLF are suspected, request `show_whitespace=true`; its
body is an inspection representation, not silently normalized content. Use
`encoding="escaped"` on `file_write` or `file_patch` when copying that
representation back. The escaped form supports `\t`, `\r`, `\n`, and
`\\`; literal backslashes must therefore be escaped as `\\`.

`file_patch` tries an exact match first. If that misses, its default
`auto_heal=true` compares line endings and horizontal whitespace only; it
writes only when exactly one candidate is found, reports `healed: true`, and
preserves the current file's line-ending convention in the replacement. It
leaves horizontal whitespace in `new_string` as supplied; use the escaped
encoding when the replacement itself must contain exact tabs. Set
`auto_heal=false` when the edit must remain byte-exact, or when whitespace may
be meaningful data rather than formatting.

```text
# GOOD — inspect, then patch with the same byte-level representation
file_read(path="styles.css", show_whitespace=true)
  → line_ending: crlf, tabs: 1, carriage_returns: 3
  → a {\r
    \tcolor: red;\r
file_patch(path="styles.css", encoding="escaped",
  old_string="a {\r\n\tcolor: red;\r\n}",
  new_string="a {\r\n\tcolor: blue;\r\n}")

# BAD — reconstructing the context with spaces and LF from a visual guess
file_patch(path="styles.css",
  old_string="a {\n  color: red;\n}", ...)
```

A `PATCH_CONTEXT_NOT_FOUND` error includes the current SHA-256, whitespace
statistics, and a nearby excerpt with `\t`/`\r` visible. Re-read that
version before retrying; do not convert grep line numbers into file-read byte
offsets.

The provider receives the current `Toolbox.ListTools` roster. `web_answer` is
listed only when configured, `generate_image` is listed only when an image
generation model is set in Settings, while `subagent`, `subagent_steer`,
`subagent_stop`, and `subagent_wait` are listed only when an ACP agent is
enabled. `delegate` is listed whenever the build supports internal
delegation, and is filtered out of the delegate agent's own roster (no
recursion).

### Dispatcher families

`skill`, `memory`, `docs`, and `memory_project` are **dispatcher tools**: one
advertised tool per family whose required `op` field selects the action.
Root+op is the SINGLE naming layer — the roster, execution routing, persisted
history, hydration checkpoints, and internal callers all use exactly this
form. There are no per-verb aliases: a call named like an old verb
(`memory_save`, `docs_read`, …) is simply an unknown tool.

Ops per family:

- `skill`: `list {limit?}` (returns `owned_by`+`shadowed` flags for path
  resolution); `search {query,limit?}`; `save {name,content,description?,id?,path?}`
  — with `path` set, writes a support file (`references/…`, `scripts/…`)
  inside an existing skill. Skill files are read with `file_read` and listed
  with `file_list` (see `docs(op="read", id="skills")`).
- `memory`: `save {content,category?,project?,task?,tags?}` is idempotent for
  exact normalized duplicates; `replace {target,content,old_text?,id?}` edits
  the primary document or one fragment; `search` is BM25-ranked with metadata
  filters; `list`; `delete {id}`.
- `docs`: `search {query,limit?}`, then `read {id}`.
- `memory_project` (only when a workspace is set): `query` (AND `topic` /
  `kind` / `related` / `id`; `archive` / `full` / `limit` optional), `list`,
  `read {kind|id}`, `admit {kind,content,id?}`, `skip {reason}`, `archive {id}`,
  `lint`. See `docs(op="read", id="memory-project")`.

## Workflow routing

Use the smallest sufficient workflow. One-step answers and lookups do not need
a TODO. Multi-step, asynchronous, or cross-turn work does.

Good examples:

    docs(op="read", id="automation")
    skill(op="search", query="release checklist")
    file_read(file_path="/home/user/.config/nusashell/skills/release-checklist/SKILL.md")
    web_search(query="current Go release notes")
    web_fetch(url="<URL selected from web_search>")
    ask_question(question="Which deployment target should I use?", options=[...])

Bad examples:

    todo(items=[{"id":"1","content":"Define MCP","status":"in_progress"}])
    skill(op="list")
    web_fetch(url="<guessed URL>")
    # ending the turn with a plain-text question — auto-continue will keep going

Use the skill catalog before repeating a `skill(op="list")` call. If the
user says skills or plugin state changed, refresh with `skill(op="search")` or
`mcp_list`. The built-in tool catalog always comes from `tools[]`. The MCP schemas and refs come from `mcp_search`.

## Todo brief quality

The brief is the task's working note and the plan file ACP subagents read.
Write it like a plan another engineer could execute from.

Good brief (concrete, verifiable, path-grounded):

```markdown
## Objective
Add a `clear_brief` flag to the todo tool so the agent can delete the plan.
Constraint: `brief: ""` must keep meaning "no change" (KISS, no silent break).

## Done when
- `todo(clear_brief=true)` empties the brief and deletes the plan file
- `todo(brief="")` in patch mode leaves the brief untouched
- go test ./infrastructure/jsonstore/... ./infrastructure/tools/... passes

## Findings
- Tool handler: infrastructure/tools/toolbox.go execTodo (~line 1356)
- Store: infrastructure/jsonstore/todo_store.go SetBrief
- ValidateBrief (domain/todo.go) requires only Objective + Done when

## Approach
1. Add ClearBrief to ConversationTodoPort (application/ports.go)
2. Implement mirror delete in TodoStore
3. Wire clear_brief in execTodo, return plan_path
```

Bad brief (prose only, nothing verifiable, no paths):

```markdown
## Objective
Improve the todo tool.

## Done when
Everything works better.
```

Update the brief when findings change the approach — a stale Approach that
contradicts Findings misleads the next round (and any subagent reading the
plan file).

## Output format

All built-in tools return output in **YAML frontmatter + JSONL body** format:
a YAML front matter block delimited by `---` lines (structured metadata like
`count`, `status`, `query`) followed by zero or more JSONL lines — one JSON
object per line. Each JSONL line is a self-contained record the agent can
parse independently. This is consistent across all built-in tools. MCP tools
called via `mcp_call` return whatever the MCP server produces (format depends
on the server).

Example:
```
---
count: 2
status: ok
---

{"id":"frag_1","category":"user","content":"prefers Indonesian"}
{"id":"frag_2","category":"project","content":"Go + Clean Architecture"}
```
MCP plugin tools are NOT advertised in the tool list — the tool list must
stay stable for the lifetime of a conversation so the provider prompt cache
(OpenAI / Claude) is not invalidated. The agent discovers and calls MCP tools
via the universal `mcp_search` + `mcp_call` pair, which works on every
provider; `mcp__<server>__<tool>` names are not callable:

1. `mcp_search(query="read file")` → returns `{"ref":"nusashell.files:read","name":"read","server":"nusashell.files","description":"Read a text file..."}` — compact, no inline schema
2. `mcp_call(ref="nusashell.files:read", arguments_json="{\"path\":\"/etc/hosts\"}")` → executes the tool

`tool_list` and `tool_schema` return the same `ref`-shaped definitions for
inspection and exact schema lookups.
If `mcp_call` returns `STALE_TOOL_REF`, the server was disabled or restarted
since the search; run `mcp_search` again and retry.
When at least one ACP agent is enabled, the interactive toolbox also advertises
`subagent`, `subagent_steer`, `subagent_stop`, and `subagent_wait`. ACP agents
do not receive this conversation, NusaShell MCP plugins, or shell meta-tools.
Pipeline `agent:` steps hide those four tools entirely so a fail-closed
permission prompt cannot stall an unattended run.

## Harness announcements

`announcement` tool results are injected by the NusaShell harness — the user
never types them, and the tool is never advertised in `tools[]`. Treat each
one as runtime state, never as a user request. Four notice types exist,
differentiated by their args `type` and result text:

- Backend restart: the runtime came back up; some MCP plugins may need
  `mcp_enable` again.
- `type: "auto_continue"`: the todo-driven chain continued into this turn
  because open TODO items remain. Resume per the notice, using the
  conversation, current runtime state, and a fresh `todo_list` result as the
  source of truth.
- Interrupted response: the previous response was cut by a transient upstream
  failure; continue it from exactly where it stopped without repeating prior
  text.
- `type: "workspace_changed"`: the user picked a new workspace for this
  conversation. Args carry `from` (previous path, omitted when none) and
  `to` (new absolute path). A `file_read` of `<workspace>/AGENTS.md` is
  injected in the same synthetic turn when that file exists — use it as
  project instructions for subsequent file tools. Do not re-read AGENTS.md
  unless it changes.

Good: on an `auto_continue` announcement, reconcile `todo_list`, mark the
next item in-progress, and continue working without acknowledging the notice.

Good: on a `workspace_changed` announcement, treat the new path as the
active workspace, follow the accompanying AGENTS.md `file_read` if present,
and continue the user's latest message without acknowledging the notice.

Bad: replying "Thanks for the announcement!" or attributing it to the user
("as you asked, I continued...") — the user never wrote it. A newer real user
message always wins; if the user said "stop" or "berhenti", stop immediately
and preserve the open TODOs.

## User decisions

A material choice the user must make (target, approval, preference) is an
`ask_question` call. The turn blocks until they answer. Auto-continue does
not inspect assistant prose: ending a turn with a `?` does not pause the
chain while open TODOs remain.

Good:

    ask_question(question="Which file should I edit?", options=[{"id":"a","label":"main.go"},{"id":"b","label":"handler.go"}])

Bad:

    Which file should I edit?

    ask only in the assistant text, then end the turn

## ACP subagents

`subagent` fans a self-contained brief out to 1–6 parallel ACP sessions
(process-wide cap 8 live runs). Pass absolute paths. `workspace` overrides the
conversation workspace for new spawns only — an already-running session keeps
the directory it started with. The tool is always async: it returns run ids
with `status: "starting"` immediately and the parent agent is free to
continue other work. When a subagent finishes, the original tool call
transitions to `ok`/`fail` with a brief terminal status and a synthetic
`subagent_result` tool call carrying the full result is injected at the
next steer-style tool-round boundary (or a new parent turn if idle)
so the parent processes the result without a user message. While any subagent is running, the
parent's auto-continue chain pauses with reason
`awaiting-background-jobs`. Completed run transcripts are persisted per
conversation under `conversations/<conversation_id>.acp/`. Permissions are auto-allowed
(orchestrator delegates authority). The user can peek the transcript
from the Agent dock / drawer / popup. Unattended pipeline agents never
see these tools.

`subagent_wait` blocks until a run reaches a terminal state (completed,
failed, cancelled) or the timeout elapses. For a terminal result, it persists
the full run before returning. The result contains `status`, run id, workspace,
`output_path`, and only the last meaningful text turn (or a compact
failure/cancellation fallback). If the run produced no text, the final thought
may be used as the bounded fallback. A timeout can return a still-running status
without `output_path`. Intermediate progress and other thought, tool, plan,
status, and usage chunks stay in the terminal JSON; use `file_read` only when that detail
is necessary. The Agent drawer receives live transcript events separately.

`subagent_steer` and `subagent_stop` acknowledge the current run status.
Their provider-facing results are bounded and omit intermediate transcript
noise.

## Native web research (searchwire)

NusaShell ships with built-in web research tools powered by
[searchwire](https://github.com/jahrulnr/searchwire). These are native tools,
not MCP plugins — they work with zero configuration and no installed plugin.

- **`web_search`**: metasearch across Brave, Serper, Tavily, Startpage,
  Wikipedia, and GitHub. No API key required for the default path (HTML
  scraping + public APIs); Serper/Tavily register when their API key is
  set (Settings → Web Search, or the `SERPER_API_KEY`/`TAVILY_API_KEY`
  env vars). Settings → Web Search also picks a routing strategy: auto
  merges all sources, round-robin/random rotate one API-keyed provider
  per query, or a bare provider name pins the query to that source.
  Returns ranked, deduplicated results with snippets.
- **`web_fetch`**: fetches a URL and returns readable text. Supports HTML
  (nav/footer/aside/form stripped, `<pre>`/`<code>` preserved, links
  collected, `og:title`/`<h1>` title fallbacks), JSON (pretty-printed;
  invalid JSON returned raw), XML/RSS/Atom (tag-stripped, newlines
  preserved), Markdown/CSV/plain text (newlines preserved). Non-UTF-8
  charsets are decoded. Surfaces `ETag`/`Last-Modified`/rate-limit
  headers, redirect count, and a `[truncated]` marker. The in-band body
  is capped at ~32KiB; when larger, YAML includes `overflow_path` (full
  extract under the platform temp dir) and `next_offset_bytes` for
  `file_read`. Non-2xx returns
  `Retry-After` (seconds) and parsed JSON error envelope when present.
- **`web_answer`**: web-grounded LLM answer (optional). Only
  registered when a vendor and API key are configured in Settings → Web Answer.
  This is a separate config from the chat providers — pick a searchwire-supported
  vendor (Brave, OpenRouter, OpenAI, Perplexity, Anthropic, xAI) and enter its
  API key manually. The key is stored in the credential store, not in settings
  JSON. An optional model/preset override can be set per vendor.

Use web research for material factual claims that are current, disputed,
consequential, unfamiliar, or likely to have changed. Prefer official or primary
sources and cross-check unstable claims. Do not browse for pure arithmetic,
logic, fictional premises, or facts already observed through an authoritative
local tool.

Good example:

    web_search(query="official release notes for the detected framework version")
    web_fetch(url="<official result selected from web_search>")
    web_answer(question="Synthesize the verified sources and their trade-offs")

`web_answer` is optional and follows source discovery. It does not replace
`web_fetch` for consequential claims.

Bad examples:

    web_fetch(url="<guessed URL>")
    web_answer(question="Is the user's assumption definitely true?")

When evidence remains incomplete, report the claim as unverified and separate
observed or sourced facts from assumptions and inferences.

## Image generation

`generate_image` is a client-side function tool. The active chat model
orchestrates; the image backend is the auxiliary model from Settings →
Image generation (OpenAI Images or OpenRouter Image API). It is not
listed until that setting is configured.

The UI shows the print as soon as the tool completes. Do not embed the
image again as Markdown, a data URL, or a file link.

Good examples:

    generate_image(prompt="a wooden fishing boat in a night harbor, sea-glass water")

    generate_image(prompt="same boat at dawn, watercolor", referenced_image_paths=["/home/user/.config/nusashell/attachments/conv_1/gen-tc_abc.png"])

Bad examples:

    generate_image(prompt="a cat")
    # then rendering `![cat](data:image/png;base64,...)` in the assistant text

    generate_image(prompt="edit the last image")  # missing referenced_image_paths

    generate_image(prompt="logo", referenced_image_paths=["logo.png"])  # relative path is rejected

Pass absolute `file_path` values from earlier `generate_image` YAML output
or from user attachments. `n` is clamped to 1–4. At most two
`generate_image` calls run at once per process.

## MCP tools

MCP tools come from each plugin's MCP server with the server's own input
schema. Discovery (`mcp_search` / `tool_list`) returns a `ref`
(`<server>:<tool>`, e.g. `Files:exec`) and execution goes through
`mcp_call`. The shell connects to the server on first use (stdio) and keeps
the connection for the process lifetime; re-saving or deleting the server
drops the connection.

### MCP tool discovery workflow

MCP tools are not advertised in `tools[]`. Use the universal `mcp_search` +
`mcp_call` pair to discover and execute — never guess a tool name or schema.

Good example:

    mcp_search(query="read file")                # → {"ref":"nusashell.files:read","name":"read","server":"nusashell.files","description":"Read a text file..."}}
    tool_schema(server="nusashell.files", tool="read")   # full schema when argument fields are unclear
    mcp_call(ref="nusashell.files:read", arguments_json={"path": "/home/user/a.txt"})   # object form (canonical)

Bad examples:

    mcp__files__read_file({path: "a.txt"})       # not in tools[], not callable

    mcp_call(ref="nusashell.files:read", arguments_json="{}")  # empty payload — the tool requires path

    tool_list()                                   # called every round to re-check
