# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
