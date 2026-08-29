# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Thinking pulse no longer sticks on finished rounds.** `is-streaming` is gated on the current round still being in the thinking phase (live reasoning, no text, no tools). Starting a tool, emitting text, or opening the next round seals prior Thinking disclosures.
- **Exec terminals stream live output.** Round-stream tool deltas that arrived before the card existed were dropped, so the UI stayed on "… waiting for output" until `agent.tool.completed`. Pending chunks are kept and flushed when the card mounts; the first delta can also create the running card.
- **Narrow agent windows no longer overflow tool/reasoning rows.** Collapsed tool meta ellipsizes instead of stretching the thread; the bubble/stack chain uses `min-width: 0`.

### Changed

- **Auto-continue no longer stops on a trailing `?`.** A plain-text question is not a user-decision gate. Only `ask_question` blocks the turn; while TODOs remain, the chain continues and the continue prompt tells the model to call that tool.
- **Live agent deltas moved from WebSocket to per-round SSE streams (`GET /stream`).** This is a **breaking wire change**: `agent.message.delta`, `agent.reasoning.delta`, and `agent.tool.delta` WS events are removed. The frontend now opens one SSE stream per round (`?run_id=&message_id=&after=<seq>`) on `agent.turn.started`, receives `round.delta` frames (`seq`, `kind` = text | reasoning | tool) and a terminal `round.done` frame (`state`, `usage`, `next`). `after=` replay makes reconnects, room switches, and page reloads self-healing: the server stages live round content in an in-memory registry (bounded, sealed TTL) and the round is committed to the conversation store atomically at seal, so snapshots never see torn mid-round state. `next` chaining carries tool-loop and auto-continue rounds forward, removing the previous room-buffer/pending-event workarounds. The WebSocket keeps signaling and lifecycle events (`agent.turn.started`, `agent.tool.started/completed`, `agent.turn.done/error`, steer, ask, compaction).

### Added

- **PWA-grade shell: installable, offline-capable, mini window.** The web
  app now ships a web app manifest (standalone display, any + maskable
  icons) and a service worker (`sw.js`, network-first with cache fallback;
  never caches `/rpc`, `/ws`, `/local-file`, `/sounds`, `/plugins/*`), so the
  shell boots from cache and shows a full-window offline state while the
  local backend is down. The page also hands its own asset list (links and
  module scripts parsed from the DOM, plus fonts parsed from fonts.css) to
  the service worker on first visit, so offline reload works after a single
  load — not only after a second load. The offline mascot moved out of the
  Agent view into a body-level `#offline-screen` overlay covering every view:
  an explicit offline verdict shows it immediately, persistent WS failure
  states cover after a 10 s grace window (quick reconnects never flicker),
  recovery hides it instantly. A title-bar install button appears only while
  the browser offers installation (`beforeinstallprompt`). A title-bar
  mini-window button opens picture-in-picture on Chromium by moving the live
  agent thread into an always-on-top Document PiP window (moved back on
  close; streaming keeps working because JS references are preserved), with
  a universal popup fallback (`?mini=1#agent`, shell chrome hidden).
  `.webmanifest` is now served as `application/manifest+json`.

### Changed

- **Hydration checkpoints hold a stable transcript position.** The synthetic
  runtime-hydration exchange is now inserted immediately before the current
  turn's assistant placeholder instead of being appended to the end of the
  conversation, making `system prompt → user → hydration → assistant` the
  deterministic order in every scenario: new room, existing chat, and
  post-compaction epoch. Round-2+ requests now extend the exact round-1
  prefix instead of shifting the checkpoint behind the model's own output,
  restoring intra-turn prompt-cache reuse across tool rounds. The injection
  path also persists once instead of twice.

- **Built-in tools deduplicated into dispatcher families.** `skill`,
  `memory`, `docs`, and `ci_pipeline` are now one advertised tool per family
  with a required `op` field (e.g. `memory` + `op=save`), replacing 15
  provider-facing schemas with 4 and cutting prompt cost on every request.
  Single naming layer everywhere: a call is stored exactly as emitted
  (`{name:"memory", args:{op:"save",…}}`), and history, learning
  classification, hydration, review agents, and UI rendering all use the
  same root form. There is no alias machinery — a call named like a retired
  per-op verb (`memory_save`, `docs_read`) is simply an unknown tool and
  fails loud; pre-migration conversations keep their stored strings, but
  replaying those specific calls errors loudly by design. Unknown or missing
  `op` fails loud with the valid list. Hot-path (`exec`, `file_*`, `web_*`,
  `mcp_call`), process-control (`ci_run/wait/cancel/steer`,
  automation/schedule), invariant-enforcing writers
  (`artifact_create/update` — frontend card contract), and privileged MCP
  gates stay typed by design.
  See `docs/design/tool-dispatchers.md`.

- **`todo` tool no longer echoes items and brief back to the agent.**
  The tool result is now a compact acknowledgment (summary counts only:
  `ok`, `conversation`, `total`, `pending`, `in_progress`, `completed`).
  The full item list and brief are not echoed back — the agent just sent
  them, and the UI receives the complete list via the `agent.todo.updated`
  event. This saves tokens on every todo call (the brief alone can be
  ~10k tokens).

- **Review agent uses hydration tool instead of flat transcript dump.**
  The background review agent now calls `review_transcript` to get the
  conversation as structured JSON (proper roles, nested tool calls, tool
  results) instead of receiving a flat text blob as a user message. The
  hydration tool is review-only — not registered in the global Toolbox.
  This improves extraction quality because the LLM sees the conversation
  semantically, not as formatted text it must parse.

- **Skill nudge trigger added.** A second review trigger fires based on
  tool-call count (`skill_nudge_interval`, default 15), independent of the
  existing turn threshold (`learning_review_threshold`, default 10). This
  catches tool-heavy coding sessions that don't reach the turn threshold.
  Both triggers share the same cooldown gate. Adjustable from Settings UI.

- **Review model override resolution recorded in trajectory.**
  `applyReviewModelOverride` now records a `review_model` trajectory event
  with the requested and resolved model names, making override failures
  visible in the learning log instead of silently logging a warning.

- **Review "started" event recorded in trajectory.** The review agent now
  records a `started` trajectory event with the resolved model name when a
  review begins, alongside the existing `done`, `skipped`, and `error`
  events.

- **Windows shell-selection guidance on `exec`.** Tool description and
  `resources/agent/docs/tools.md` now steer Windows users to pick shells via
  the `shell` parameter instead of invoking cmd.exe/powershell.exe inside a
  bash command line — MSYS path conversion mangles drive-letter paths such
  as `Z:/x`.

### Fixed

- **Windows rename races in atomic writes and moves.** `file_write`,
  `file_patch`, and `file_move` rename a freshly closed temp file into
  place; on Windows, antivirus scanners and search indexers routinely hold
  that file for a few milliseconds, so an otherwise-valid `os.Rename` fails
  with `ERROR_SHARING_VIOLATION`, `ERROR_LOCK_VIOLATION`, or
  `ERROR_ACCESS_DENIED`. Renames now retry briefly (bounded to 4 attempts,
  10 ms doubling backoff) and only for those transient errnos on `windows`,
  so permanent failures still surface immediately. The renamer, sleeper, and
  GOOS check are injected package vars — guarded by
  `TestWriteFileAtomicRetriesTransientRenameFailure`,
  `TestWriteFileAtomicNoRetryOnPermanentRenameFailure`, and
  `TestWriteFileAtomicRetryIsBounded`.

- **`ci_pipeline` ops were advertised but unreachable.** Every call to the
  dispatcher family died with `unknown ci_pipeline_list op "list"`:
  `executeFamily` had no case for the resolved keys while the real handlers
  sat in an unreachable branch of the automation executor (the family root
  is intercepted before that executor runs). The three ops now live in
  `executePipelineOp`, called only with the validated bare op — direct calls
  with resolved keys (`ci_pipeline_list`) stay unknown tools, so no alias
  door exists. Guarded by `TestAllAdvertisedFamilyOpsRoute`, which executes
  every advertised family root+op through `Toolbox.Execute`.

- **Stale per-op tool names scrubbed from fixtures and docs.** Frontend test
  fixtures (`e2e.test.mjs` hydration slots and scripted tool calls,
  `agent-render.test.mjs`, `learning.test.mjs`), the RPC golden DTO, and the
  `scan-ui-docs` comment now use the single-layer root+op form
  (`name:"memory", args:{op:"search"}`) matching what production persists.

### Removed

- **SSE `/events` endpoint removed (dead route).** The frontend never used
  SSE, and `app.info` no longer advertises the `sse` transport or the
  `transports` list. Transport tests that previously streamed events over
  SSE now cover the same behavior over WebSocket.

### Added

- **Image generation tool.** `generate_image` is a built-in client-side
  tool (advertised only when Settings → Image generation is set). The
  chat model orchestrates; OpenAI Images, OpenRouter's dedicated Image
  API, or a signed-in Codex ChatGPT plan (`POST …/codex/images/generations`
  and `/images/edits`, OAuth already used for chat) renders the print.
  Import models also fetches OpenRouter `GET /images/models` and tags
  `gpt-image-*` / `dall-e-*` as `kind: image`. Codex providers seed
  `gpt-image-2` (and `gpt-image-1.5`) because Codex `model/list` has no
  image catalog. Results persist under
  `attachments/<conversation>/gen-<toolCallID>.*` without stuffing base64
  into conversation JSON. The agent thread shows a developing-tray card
  while the tool runs, then a proof with zoom and download. Tool-result
  images now survive the Responses adapter (`function_call_output` can
  carry `input_image` items). Codex image 429s with
  `x-codex-active-limit: image_gen` fail over through the existing
  multi-account router.
- **Automation engine and CI runner.** Workspace `.nusashell/pipeline.yaml`
  plus saved once/every/when automations, a local executor, SQLite
  `automation.db`, agent tools (`ci_*`, `automation_*`, `schedule_*`,
  `wait_until`), RPC (`ci.*` / `automation.*`), and an Automation sidebar
  view. Waiting parks the run without occupying a runner. Disabled MCP
  providers block dependent automations instead of failing them.
- **ACP spawn-only subagents.** Register generic Agent Client Protocol
  binaries (command + args + env) in Providers. They never appear in the
  composer. The parent agent delegates with `subagent` / `subagent_steer` /
  `subagent_stop` / `subagent_wait` (advertised only when at least one ACP
  agent is enabled). Live runs show in an Agent dock, right-hand drawer, and
  peek popup, with fail-closed permission prompts, dynamic workspace binding,
  and parallel spawn (1–6 per call, 8 live cap). Pipeline `agent:` steps
  do not advertise or execute those tools (approval must not block unattended runs).

### Changed

- **Night-harbor workbench.** Replaced the acid-lime shell with a sea-glass
  teal palette, island-chain brand mark, and a tide line on the title bar.
  Hardcoded lime/ink colors now flow from CSS tokens. Empty agent threads
  offer starter prompts, the composer dock highlights while focused and
  while steering, Rooms reopens the conversation list on narrow widths,
  and the sidebar data-directory path is visible again.
- **Shell interaction.** Dialogs dismiss on Escape with a focus trap.
  Toasts pause while hovered. Ctrl/Cmd+K or `/` focuses the current view's
  search box; Ctrl/Cmd+N starts a new agent conversation when you are not
  typing.
- **Unified plugins model.** MCP servers and installed plugins are now one
  concept: every entry is a plugin stored under `plugins/<id>/manifest.json`
  (a manual MCP server is a plugin manifest with an `mcp` block and no `ui`
  block). The old `mcp.servers.*` RPC methods, the `MCPServer` domain type,
  and the `mcp-servers.json` store were removed; RPC is now `plugin.list`,
  `plugin.save`, `plugin.test`, `plugin.stop`, `plugin.delete`,
  `plugin.uninstall`, `plugin.catalog`, `plugin.install`.
- **`mcp_list` lists all plugins.** The agent-facing `mcp_list` tool and the
  hydration snapshot now include every plugin (running or idle) with its
  runtime state, so the model can see configured-but-stopped servers; the
  JSON key is `plugins` (was `running`).
- **Manual MCP server editing via plugins UI.** Add/Edit MCP in the Plugins
  view now writes a plugin manifest (`plugin.save`) instead of a separate
  MCP server record.

### Fixed

- **Emergency compaction re-injects hydration.** After a successful overflow
  compact-and-retry, the next provider round recreates the runtime checkpoint
  (date, memory, skills, MCP, tools) instead of continuing the turn blind.
- **Overflow detection is explicit.** Emergency compaction no longer treats
  a generic `input_tokens` substring as context overflow, and it refuses to
  compact unless the local token estimate already exceeds the compaction
  trigger — so a schema 400 cannot archive the transcript.
- **Multi-pass compaction tracks summary growth.** Later summarization passes
  shrink the message chunk as the running summary grows, so the last pass
  does not overflow the model window.
- **Crash recovery for running conversations.** Loading the store converts
  leftover `status: running` conversations to idle and marks in-flight
  assistant work interrupted, so a restart cannot leave a room permanently
  busy.
- **In-process orphaned turn recovery.** A turn that exits without a terminal
  state (panic recovered by `goSafe`, or an early return that skipped
  `failTurn`/`interruptTurn`) is healed immediately by the `runTurn` defer:
  the conversation is reset to idle, in-flight assistant messages are marked
  interrupted with a visible error, and a `turn.error` event is emitted so the
  UI shows what happened instead of hanging silently. `agent.turns.start` and
  `agent.turns.retry` also heal an orphaned running conversation (status
  "running" but no active run) before starting a new turn, instead of
  returning a permanent 409 "conversation is busy".
- **Codex compact replays assistant text.** Server-side Codex compaction
  now includes assistant message text in the replay, not only user turns.
- **Review loop mid-failure is an error.** If `Complete` fails inside the
  background review loop, the trajectory records `error` instead of `done`.
- **Compaction archives omit hydration.** Hydration checkpoints are stripped
  before archive and `Compact`, so scroll-back chunks no longer duplicate
  synthetic runtime snapshots.

- **MCP autostart at process boot.** Plugins with `mcp.autostart` are
  connected when the Go process starts (and immediately when the toggle is
  turned on), so automations and agent tools are available without a manual
  Start. A failed connect is logged and skipped.
- **ACP workspace path containment on Windows.** Slash-rooted paths such as
  `\etc\passwd` (and Unix-style `/etc/passwd`) are treated as rooted locations
  and no longer join onto the bound workspace, so `edit_confirmed` edits
  outside the workspace prompt instead of auto-allowing.
- **ACP test helper binary on Windows.** `fakeacp` test builds now use a
  `.exe` suffix so GitHub Actions `windows-latest` can execute them.
- **ACP stdio is newline-delimited JSON.** Outbound frames are one JSON-RPC
  object per line. LSP `Content-Length` headers made Gemini CLI (`gemini --acp`)
  fail with `Unexpected token 'C', "Content-Length: …" is not valid JSON`.
  Inbound still accepts Content-Length for older helpers.
- **ACP spawn without CLI login.** When `session/new` fails with
  `Authentication required` and Providers never stored an auth method id,
  spawn and refresh catalog now report which advertised method ids to use
  (`cursor_login`, `api-key`, …) instead of a bare JSON-RPC error.

- **Provider kind-change validation ordering.** Saving a codex provider with
  a different kind now correctly fails with `VALIDATION_ERROR` (the guard now
  runs before base URL validation, which previously masked it with "base url
  is required").


- **Loopback by default.** The HTTP server now listens on `127.0.0.1` unless
  `NUSASHELL_HOST` is set, matching the documented personal-shell threat model.
- **Larger RPC bodies.** `/rpc` accepts up to 24 MiB so the documented four
  4 MiB attachments can actually be posted from the frontend.
- **Streaming HTTP client.** Provider adapters no longer apply a 60s client
  timeout to the entire SSE body; dial and response-header timeouts remain.
- **Compaction watermark.** Compaction uses `settings.compaction_threshold`,
  capped at 80% of the model context window.
- **Focused agent modules.** The agent view now separates transcript rendering,
  composer interaction, and model selection while keeping the existing browser
  API and UI behavior intact.
- **Cross-platform workspace picker.** Folder selection now uses the native
  system dialog on Windows and macOS as well as supported Linux desktop
  dialogs.

### Fixed

- **Token estimates.** Conversation size no longer includes provider usage
  totals or double-counts chronological steps, so compaction triggers on the
  stored transcript rather than last-request accounting.
- **Retry continuation.** A failed turn that already has tool calls restarts
  from scratch instead of injecting a continue prompt against an empty message.
- **Stop during tools.** Cancelled turns skip remaining tool calls and keep
  streamed reasoning on the interrupted message.
- **WebSocket writer.** The event writer exits when the bus subscription
  closes instead of spinning on a closed channel.
- **Logs live tail.** Appended log events no longer replay the whole history.
- **Conversation switch races.** Stale conversation, chunk, and active-run
  RPCs are ignored after the user switches rooms.
- **Credential file mode.** The SQLite credential store is created `0600`.
- **Static JavaScript MIME compatibility.** The asset check accepts either
  standard JavaScript media type returned by the operating system, preventing
  a Windows-only CI failure.
- **Resilient upstream recovery.** Transient provider failures now retry with
  bounded backoff and `Retry-After` support; interrupted streaming output is
  preserved and continued once without repeating completed tool work.
- **Windows Codex compact fixture.** `TestCompactServerSubprocessSuccess`
  compiles a Go fake Codex app-server instead of writing a bash script, so
  the subprocess path is executable on Windows CI.
- **Codex turn-start failover.** When every stored Codex account is
  rate-limited or circuit-open, the turn fails immediately instead of
  sending the blocked active token.
- **Codex OAuth refresh persistence.** Refreshed tokens are written to both
  the active provider key and the account-scoped credential key.
- **Provider delete credential cleanup.** Deleting a provider also removes
  `{providerID}:account:*` credentials, not only the active key.
- **Codex circuit usage snapshot.** `LimitReached` without a reset timestamp
  no longer closes an already-open circuit breaker.

## [0.1.0] - 2026-08-13

### Added

- **NusaShell Go port.** A self-contained Go application serves an
  embedded native-JavaScript frontend with multi-conversation agent chat,
  Messages, Responses, and Chat provider formats, MCP, skills, memory,
  document search, and local credential storage.
- **Workspace, context, and attachments in the agent composer.** Each active
  conversation can select a folder through the native picker, display context
  usage, and attach files to a turn.
- **Electron-aligned execution timeline.** Reasoning renders Markdown, while
  reasoning and tool calls remain collapsed by default. Long tool output scrolls
  within its panel after ten lines.
- **GitHub Actions verification.** Pull requests and pushes to `master` run
  frontend tests plus Go formatting, vet, race-test, and build gates on the
  supported operating systems.

### Changed

- **Conversation UI parity.** The conversation rail, composer, workspace
  control, context counter, attachment control, and tool presentation now
  follow the NusaShell Electron interaction model.

### Fixed

- **Timeline alignment and readability.** Reasoning entries line up with tool
  entries, and Markdown content in reasoning is rendered as formatted HTML
  instead of raw source text.
- **Windows CI formatting check.** Source checkouts now preserve LF line
  endings, so `gofmt` evaluates the repository content consistently on every
  supported operating system.
