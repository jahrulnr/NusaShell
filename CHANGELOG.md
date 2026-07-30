# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.0.33] - 2026-07-31

### Added

- `files_copy` tool: copy a file or directory recursively to a new path.
  Destination parent directories are created automatically.
- `files_grep` tool: search file contents by regex pattern (like grep), with
  optional glob filter to narrow by file name (e.g. `*.js`). Only text files
  are scanned; results include path, line number, and matching line content.
- `files_patch` tool: replace the first occurrence of `old_string` with
  `new_string` in a file. Safer than `files_write` for targeted edits.
- `files_append` tool: append content to the end of a file, creating it if it
  does not exist. Parent directories are created automatically.

### Changed

- All files tool schema descriptions now explicitly mention "files plugin root
  (user home directory by default)" instead of the ambiguous "root", making it
  clear what paths are relative to.
- ENOENT errors from files plugin operations now include a hint with the actual
  root path (e.g. `Files plugin root is "/home/user"`) so the agent does not
  have to guess what root is.

### Fixed

- Agent UI streaming: reasoning blocks from multiple rounds now appear as
  separate sections instead of merging into one. Previously, all reasoning
  deltas across rounds were appended to a single block. Now a new reasoning
  block is created whenever reasoning resumes after a tool call ends, so the
  visual order matches the actual flow: thinking → tool → thinking → tool →
  response.
- Agent UI streaming: the renderer never subscribed to WebSocket events on
  first connect because `activeSubscriptions` was empty and the `onopen`
  handler only re-subscribes when it is non-empty. The 500 ms `setTimeout`
  fallback silently failed if the WebSocket was not yet connected. Now
  `activeSubscriptions` is pre-seeded with `"*"` before `connectWs()` so the
  subscribe request is always sent in `onopen`.

## [0.0.32] - 2026-07-30

### Fixed

- `OpenAiCompatibleAgentProvider` now falls back from the `responses` API to `chat/completions` within the same provider when the responses endpoint returns 404, 405, or a 4xx/5xx body indicating the endpoint is not supported. This fixes OmniRoute gateway turns where an upstream provider does not support `/responses` — the turn retries via `/chat/completions` instead of failing.
- `OpenAiCompatibleAgentProvider` connection and timeout errors now include the attempted endpoint URL for easier diagnostics.

### Added

- First-party Mail plugin with an original three-pane mailbox UI and eight
  read-only `mail_*` MCP tools for account discovery, connection tests,
  mailboxes, inboxes, search, and MIME message reading.
- Multi-account IMAP/SMTP settings managed by the Mail UI, encrypted with
  Electron `safeStorage`, and injected into the Mail MCP process only at
  runtime.
- Manifest-driven plugin window sizing, entry points, resize behavior, and
  close lifecycle so full-screen plugin surfaces can use their declared
  presentation.
- Browser fixture, MCP service tests, credential-store tests, and plugin-window
  option tests for the new Mail integration.

### Security

- Mail credentials are excluded from renderer responses, MCP schemas and tool
  output, plugin manifests, and persisted plugin metadata.
- Mail server configuration requires TLS or STARTTLS with certificate
  verification enabled by default; message bodies are bounded before reaching
  the agent or UI, and formatted alternatives stay inside a restricted
  document without script, form, frame, API, or shell access.

### Fixed

- Mail account IPC is authorized during the plugin's initial page load, so a
  configured account can be read immediately without weakening plugin-window
  sender validation.
- Mail now selects the first enabled account on initial open and loads that
  account's folders and inbox instead of leaving the account selection empty.
- MCP tool failures now preserve the server's safe error text through the
  transport adapters; Mail also surfaces IMAP response details such as
  authentication rejection and records failed tool names in the MCP log.
- Home now renders plugin-local `file://` PNG artwork instead of a generic
  fallback glyph, and Mail uses its dedicated launcher artwork inside the
  same icon plate as other plugins.
- Packaged desktop artifacts now preserve the expected
  `resources/plugins` layout, so bundled plugins and their local
  artwork remain discoverable outside development.
- Mail account rows now expose an explicit edit action with account deletion
  available in the editor, and Gmail setup and authentication failures direct
  users to replace regular account passwords with Google App Passwords.
- Plugin windows now fit their requested dimensions to the active display work
  area, while Mail switches to a responsive two-pane/read view on narrow
  windows instead of clipping content beyond the screen.
- Home normalizes transparent padding in PNG plugin artwork and gives image
  and emoji icons the same visual plate, preventing mixed icon sources from
  appearing at unrelated sizes.
- Mail now renders formatted HTML alternatives, including inline styling and
  HTTPS images, inside a sandboxed document that cannot run scripts, submit
  forms, open nested frames, connect to APIs, or access the shell bridge.

### Attribution

- Mail service structure was adapted from `codefuturist/email-mcp` at pinned
  revision `99ce431aa81dd4cafc2879bd35b6ee3acd0f2d74`; upstream source, license,
  and the scope of NusaShell's changes are recorded with the plugin.

## [0.0.31] - 2026-07-30

### Added

- Managed local agent skills library with safe `.skill`/`.zip` installation,
  bounded filesystem access, UTF-8 editing, binary metadata viewing, and
  managed-copy deletion.
- Three-pane Skills workspace in the desktop launcher for searching skills,
  browsing package files, and editing package text.
- Read-only `skill_list`, `skill_search`, and `skill_read` agent meta-tools.

### Security

- Skill package extraction rejects traversal paths and symbolic links, limits
  archive entry and expanded sizes, and prevents reads or writes outside the
  selected managed skill.

## [0.0.30] - 2026-07-29

### Added

- `postinstall` script in `apps/desktop/package.json` runs `electron-rebuild -f -w better-sqlite3` automatically after `pnpm install`, ensuring the native SQLite module is rebuilt for Electron's ABI without manual steps.
- README "Prerequisites" section documenting Node.js 20+, pnpm 11+, and native build tools (`python3`, `make`, `g++`) needed for `better-sqlite3` with per-OS install instructions.
- README "Quickstart (Desktop App)" section with the `pnpm install && make dev` flow for the Electron desktop app.

### Changed

- Desktop `maxToolRounds` raised from `8` to `50` in `apps/desktop/src/main/index.ts`, matching `DEFAULT_MAX_TOOL_ROUNDS` in the application package so the agent turn loop can actually use the full tool-round budget.
- README "Project status" and "Repo layout (today)" updated to reflect implemented packages (application, infrastructure, transport-ws, contracts, plugin-sdk, backend, desktop) instead of stale "stubs" labels.

## [0.0.29] - 2026-07-29

### Fixed

- Plugin windows (e.g. Notes) could not be reopened after closing: the `ready-to-show` handler was registered after `await loadURL`, so on fast/cached loads the event fired before the handler and the window stayed hidden. The handler is now registered before `loadURL` with a fallback `win.show()` after load completes.
- `openPluginWindow` and `closePluginWindow` now guard against destroyed `BrowserWindow` references lingering in the plugin window map.

## [0.0.28] - 2026-07-29

### Fixed

- Notes built-in MCP server (`plugins/notes/mcp/server.js`) now persists notes to `notes.json` in the plugin directory and restores them on startup, so notes created by the agent survive process restarts and plugin window closes.
- Notes plugin UI (`plugins/notes/ui/index.html`) now correctly unwraps `window.shell.callTool` results whether the backend returns a raw content array, a `CallToolResult` wrapper, or a nested `result.content` object.

## [0.0.27] - 2026-07-29

### Changed

- `AgentTurnRunner` repeated identical tool call threshold raised from `3` to `50` (`MAX_REPEATED_TOOL_CALLS`).
- `maxToolRounds` default raised to `50` and validated maximum raised to `100` across `app-config`, `container.ts`, and the WebSocket request schema so the new repeat threshold can actually be reached.
- `AgentTurnRunnerDeps` exposes an optional `defaultMaxRepeatedToolCalls` override for tests and advanced callers.
- Desktop preload now exposes `window.shell.callTool` and `window.shell.listTools`, routing plugin iframe tool calls through the existing `tool:call` / `tool:list` IPC handlers.

## [0.0.26] - 2026-07-29

### Added

- Build-time UI docs scanner (`scripts/scan-ui-docs.mjs`) that parses `apps/desktop/src/renderer/index.html` and JS source files, validates them against `resources/agent/docs/ui-source/ui-map.json`, and generates `resources/agent/docs/ui/*.md`.
- `resources/agent/docs/ui-source/ui-map.json` — human-maintained UI map describing all NusaShell launcher views, controls, interactions, and keyboard shortcuts.
- `pnpm scan:ui-docs` script and `prebuild` hook to regenerate UI docs before every build.
- `scripts/scan-ui-docs.test.mjs` unit tests and `MarkdownDocsIndex` integration test covering view/control extraction, validation, markdown rendering, and indexing under the `ui/` domain.

### Changed

- `AGENTS.md` now requires updating `resources/agent/docs/ui/*.md` whenever renderer source changes.
- `docs/architecture/agent-runtime.md` and `resources/agent/prompts/mcp-tools.md` mention the generated UI docs corpus and guide agents to use `docs_search` for UI questions.
- Refined agent prompts based on review: reduced `system.md` / `mcp-tools.md` redundancy, fixed `developer.md` cross-reference to `mcp-tools.md`, clarified meta-tools are always present while granted plugin tools expire at turn end, added over-discovery and ambiguous-plugin guardrails to `mcp-tools.md`, and added NusaShell runtime state guidance to `compact.md`.

## [0.0.25] - 2026-07-29

### Added
- `DocsIndexPort` interface in `@nusashell/application` — `docs_search`, `docs_list`, and `docs_read` types and contract for agent-facing documentation tools.
- `MarkdownDocsIndex` adapter in `@nusashell/infrastructure` — walks `docsRoot` for `*.md` files, chunks by second-level headings, builds a lexical keyword index, persists it to `docsIndexStorageRoot`, and exposes `search`, `listDocs`, and `readDoc`.
- `docs_search`, `docs_list`, and `docs_read` shell-owned meta-tools in `McpAgentToolGateway` — returns structured envelopes with `ok`, `data`, and `meta` (`index_ready`, `data_is_untrusted`, `truncated`, pagination `next_offset`).
- Documentation corpus seeded under `resources/agent/docs/` with `getting-started.md`, `plugins.md`, `agent.md`, `mcp-tools.md`, and `settings.md`.
- `mcp-tools.md` prompt section explaining when and how to use `docs_search`, `docs_list`, and `docs_read`.
- Unit tests for `MarkdownDocsIndex` covering index building, search ranking, list/read, chunk reads, not-found, rebuild, and missing root handling; plus `McpAgentToolGateway` tests for docs tool execution and not-configured behavior.

### Changed
- `McpAgentToolGateway` constructor accepts an optional `DocsIndexPort` dependency.
- `container.ts` wires `MarkdownDocsIndex` with `docsRoot` and `docsIndexStorageRoot` options, passes it to `McpAgentToolGateway`, and triggers a lazy background index build.
- `docs/architecture/progressive-mcp-tools.md` and `docs/architecture/agent-runtime.md` updated to document the documentation tool set and index behavior.

## [0.0.24] - 2026-07-29

### Added
- System prompt and context builder for the agent runtime: prompt files in `resources/agent/prompts/` (`system.md`, `mcp-tools.md`, `developer.md`, `compact.md`) are loaded via `FilesystemPromptLoader` and injected before conversation messages reach the provider.
- `PromptLoaderPort` interface and `injectPrompts()` service in `@nusashell/application` — prepends static and developer prompts, applies `{{current_date}}`, `{{environment}}`, and `{{available_tools}}` template substitution to `developer.md`, preserves compaction summaries, and drops stale non-summary system messages.
- `FilesystemPromptLoader` adapter in `@nusashell/infrastructure` — reads and caches prompt files from a configurable root (`promptsRoot` container option, defaults to `resources/agent/prompts`); loads `compact.md` lazily.
- `tool_list` meta-tool in `McpAgentToolGateway` — lists all tool names and descriptions from a running MCP plugin without requiring a search query, complementing `tool_search` for full tool discovery.
- `compactPrompt` option in `AgentTurnRunnerDeps` — compaction instruction loaded from `compact.md` with fallback to the built-in default.
- 9 unit tests for `injectPrompts` and `applyVars` covering template substitution, prompt ordering, compaction summary preservation, and edge cases.
- "System prompts" section in `docs/architecture/agent-runtime.md` documenting the prompt files, injection point, template variables, and fallback behavior.

### Changed
- `RunAgentTurnHandler` now accepts an optional `PromptLoaderPort` constructor dependency; loads prompts, resolves available meta-tool names for `{{available_tools}}`, and injects system messages before passing conversation to the runner. Falls back to raw messages on loader failure.
- `AgentTurnRunner` compaction instruction now uses `compactPrompt` from deps when available instead of the hardcoded string.
- `container.ts` wires `FilesystemPromptLoader` and passes it to `RunAgentTurnHandler`; `ContainerOptions` accepts optional `promptsRoot`.

## [0.0.23] - 2026-07-29

### Added
- Provider-family registry for OpenRouter, OmniRoute, 9Router, OpenAI, Claude, and hidden custom OpenAI-compatible connections, including legacy ID/host inference and provider-specific defaults.
- Model-aware runtime policy for context/output limits, tools, vision, and model-specific reasoning effort from imported `/models` metadata with conservative family heuristics.
- Bounded SSE streaming with durable final responses, centralized text-delta events, active-turn cancellation, and cancellation of in-flight MCP tool calls.
- Images and PDF attachments in durable Agent conversations with model compatibility checks and strict count, size, media-type, and data-URL limits.
- Hot-reloadable agent runtime settings for failover strategy, total attempt budget, streaming, vision, provider timeout, retry attempts, and weight.
- Provider failover with transient-only routing, global attempt budgets, successful-provider pinning, and fallback when a pinned provider becomes unavailable.
- Recovery for XML/Kimi-style textual tool calls, reasoning-only responses, malformed/empty streams, duplicate tool calls, and bounded tool-round exhaustion.
- Progressive MCP resource-template discovery and completion, plus sanitized protocol log notifications in the centralized log tail.

### Changed
- Chat requests omit empty tool fields for stricter OpenAI-compatible gateways.
- Model catalog imports now have bounded pagination, origin checks, a 30-second timeout, a 16 MiB response limit, and non-model filtering.
- Agent model selection now uses imported compatibility metadata for searchable provider, modality, context, tools, and effort badges.
- Provider cards now expose live enable/disable controls while preserving configured credentials and clearing stale selections safely.

### Fixed
- Disabled or deleted provider connections are removed from the live provider registry immediately.
- Cancelled turns are reported separately from provider failures and never persist a partial assistant response.
- Missing `/models` tool metadata is treated as unknown rather than incorrectly disabling MCP tools.

## [0.0.22] - 2026-07-28

### Added
- Agent runtime with a bounded, traceable provider → MCP tool → provider turn loop.
- MCP-only agent tool gateway: it exposes only tools from running plugins and rejects model calls outside the current allowlist.
- Provider registry and a shared adapter for OpenAI-compatible Chat Completions, Responses, and Anthropic Messages gateways.
- `agent.run` WebSocket command and `NusaClient.agent.run()` SDK API.
- Desktop Agent workspace with a durable searchable conversation rail, confirmed deletion, failed-turn retry, turn metadata, and centralized trace logging.
- MCP capability policy documenting the stable implementation track and deferred experimental/evolving capabilities for operator and agent knowledge.
- Per-plugin MCP autostart preference, persisted in installed metadata and applied best-effort during backend startup; launcher drawer toggle included.
- Progressive agent MCP discovery: bounded `mcp_list`, `mcp_enable`, `mcp_disable`, `tool_search`, and `tool_schema` catalog, with one tool schema granted per subsequent round.
- Brokered MCP prompts and resources over stdio, HTTP, and SSE transports: `prompt.list`, `prompt.get`, `resource.list`, `resource.template.list`, and `resource.read`.
- `mcp_context` progressive meta-tool for prompt listing/retrieval and bounded resource search/read without exposing MCP context controls in the UI.
- Agent provider, model, and effort pickers, plus persisted OpenAI-compatible provider settings. API keys are encrypted through Electron `safeStorage` and are never returned to the renderer.
- Multi-provider AI registry with optional default models, provider detail pages, `/models` catalog import, manual model entry, and migration from the original single-provider settings.
- Searchable Agent model picker combining every enabled provider and showing provider identity, context size, modalities, tools, and model-specific reasoning effort levels.
- Provider-specific runtime adapters for Chat Completions, OpenAI Responses, and Anthropic Messages, including native tool-call round trips.
- Focused tests for text turns, MCP tool calls, allowlist rejection, round limits, and OpenAI-compatible function-call parsing.
- Durable context compaction with recent-turn preservation and extractive fallback, plus bounded transient-provider retries with exponential jitter and `Retry-After` support.
- Environment-only `NUSASHELL_AI_STUB` test provider; stub providers and labels are excluded from the persisted production registry and every frontend surface.

### Changed
- Backend and package type checks now pass after correcting existing event dispatch and strict TypeScript issues.
- Electron Forge now builds the typed preload as the single bridge source of truth; the stale duplicate preload was removed.

### Fixed
- Configured AI provider cards and detail pages now expose confirmed deletion, removing the provider's credential, imported models, active selection, and live runtime adapter.

## [0.0.19] - 2026-07-29

### Added
- Plugin UI bridge: `window.shell.callTool(pluginId, toolName, args)` and `window.shell.listTools(pluginId)` exposed in preload for plugin UIs to call MCP tools via IPC
- IPC handlers `tool:call` and `tool:list` in main process — call backend command/query bus directly (in-process, no WS roundtrip)
- Plugin window receives `pluginId` via URL query param so plugin UI knows its own identity
- Notes plugin UI is now functional: textarea + create button calls `createNote` MCP tool, lists notes on load via `listNotes` tool, dark theme matching PoC style
- SQLite persistence wired in desktop app: set `NUSASHELL_DB_PATH` env var to activate `SqlitePluginRepository` (requires `better-sqlite3` rebuilt for Electron ABI)
- `PluginSyncService` — syncs filesystem plugins into SQLite on startup (upsert found plugins, remove stale entries)
- `@nusashell/application` added as desktop dependency for command/query type imports
- Updater IPC handlers registered in dev mode as no-ops to prevent renderer errors

### Changed
- Desktop main process defaults to filesystem plugin registry; SQLite activates when `NUSASHELL_DB_PATH` is set
- `SqliteDatabase` lazy-loads `better-sqlite3` only when instantiated, preventing SIGSEGV when the native module isn't Electron-compatible
- Container syncs filesystem plugins to SQLite when both `dbPath` and `pluginsRoot` are set

## [0.0.18] - 2026-07-29

### Fixed
- Plugin popup window now opens correctly when clicking a plugin icon
- Preload script output forced to `preload.cjs` via `entryFileNames` in Vite preload config — Electron cannot `require()` ESM `.js` files when `package.json` has `"type": "module"`
- Removed `index.js` fallback for preload path — only `preload.cjs` is valid
- `openPluginWindow` no longer blocks on `getPluginDetail` WS call (which raced with `startLocked`); uses `plugin.installPath` from `plugin.list` response directly
- `handlePluginEvent` now checks `payload.newState` (from `plugin.state_changed` events) in addition to `payload.state`
- `PluginRuntimeManager.doStart` logging fixed: `this.logger` → `this.deps.logger` with correct `LoggerPort` signature
- `StdioMcpClient.connect()` no longer hangs — added timeout and transport-close detection
- WebSocket server handles messages concurrently so `plugin.start` doesn't block `plugin.get`
- Plugin MCP server stopped via WS when plugin window closes (`keepAliveOnClose: false`)
- Hardcoded WS port replaced with `NUSASHELL_PORT` env var in window cleanup

## [0.0.17] - 2026-07-29

### Added
- Plugin installation from URL: `plugin.install` command with `source: "url"` downloads and extracts zip/tar.gz archives
- Plugin installation from local path: `plugin.install` command with `source: "local"` installs from a local directory or archive file
- Plugin uninstallation: `plugin.uninstall` command removes a plugin from the plugins directory
- `PluginInstaller` infrastructure adapter — downloads URLs, extracts `.zip` (via `adm-zip`) and `.tar.gz`/`.tgz` (via `tar`), validates manifest, copies to plugins root
- `PluginInstallerPort` application port interface for install/uninstall operations
- `InstallPluginCommand`/`InstallPluginHandler` and `UninstallPluginCommand`/`UninstallPluginHandler` in application layer
- `PluginUninstalledEvent` domain event
- `PluginInstallRequestSchema`, `PluginUninstallRequestSchema` in contracts with `plugin.install` and `plugin.uninstall` request methods
- `PluginInstallResultSchema`, `PluginUninstallResultSchema` response schemas
- `PluginsApi.install()` and `PluginsApi.uninstall()` in plugin-sdk
- Command mapper handles `plugin.install` and `plugin.uninstall` methods
- Container wires `PluginInstaller` + install/uninstall handlers when `pluginsRoot` is configured
- Auto-update via `electron-updater`: `AppUpdater` module in desktop main process checks for updates on startup (packaged only), auto-downloads, and notifies renderer via IPC
- `@electron-forge/publisher-github` configured in `forge.config.ts` for publishing to GitHub Releases
- `electron-updater` externalized in Vite main config
- Updater IPC exposed in preload: `window.shell.updater.checkForUpdates()`, `.quitAndInstall()`, `.getStatus()`, `.on(channel, cb)`
- `pnpm desktop:publish` root convenience script
- Launcher UI: "Add Plugin" modal dialog with URL and local path install flows
- Launcher UI: uninstall button in context menu and plugin detail drawer with confirm prompt
- Launcher UI: `plugin.installed` and `plugin.uninstalled` event handling with activity timeline entries and filter chips
- Launcher UI: auto-update notification banner (update available, download progress, restart-to-update button)
- Launcher UI: toast notification system for install/uninstall/update feedback
- `plugin.installed` and `plugin.uninstalled` event schemas in contracts with `EventType` and `EventSchema` discriminated union
- Client event mapper handles `plugin.installed` and `plugin.uninstalled` domain events

### Changed
- `RequestMethod` type extended with `plugin.install` and `plugin.uninstall`
- `RequestSchema` discriminated union includes `PluginInstallRequestSchema` and `PluginUninstallRequestSchema`
- Infrastructure `package.json` adds `adm-zip`, `tar` dependencies and `@types/adm-zip`, `@types/tar` dev dependencies
- Desktop `package.json` adds `electron-updater` dependency and `@electron-forge/publisher-github` dev dependency
- Removed stale `pnpm.onlyBuiltDependencies` from root `package.json` (already in `pnpm-workspace.yaml` as `allowBuilds`)

## [0.0.16] - 2026-07-29

### Added
- Electron Forge integration with Vite plugin for bundling main, preload, and renderer
- `forge.config.ts` with AppImage + deb makers for Linux packaging
- Vite configs: `vite.main.config.ts`, `vite.preload.config.ts`, `vite.renderer.config.ts`
- `@electron/rebuild` for native module (`better-sqlite3`) ABI compatibility in packaged builds
- Plugin examples bundled as extra resources via `packagerConfig.extraResource`
- Root convenience scripts: `pnpm desktop:dev`, `pnpm desktop:make`

### Changed
- Dev script now uses `electron-forge start` instead of raw `electron .`
- Main process entry changed from `electron-entry.mjs` (tsx loader) to `.vite/build/main.cjs` (Vite CJS output)
- `window-manager.ts` uses Forge Vite dev server URL in dev mode, file loading in production
- `pnpm-workspace.yaml` — added `nodeLinker: hoisted` for Forge compatibility
- `.npmrc` — added `node-linker=hoisted`
- `tsconfig.json` — includes `forge.config.ts` and `vite.*.config.ts`
- `--no-sandbox` flag added to dev script for Linux chrome-sandbox SUID fix

### Removed
- `electron-entry.mjs` — replaced by Forge + Vite build pipeline

## [0.0.15] - 2026-07-29

### Added
- Electron desktop shell scaffold in `apps/desktop`:
  - Main process (`src/main/index.ts`) embeds backend in-process via `bootstrap()`
  - Window manager creates launcher + plugin BrowserWindows
  - Preload script (`src/preload/index.cjs`) exposes `window.shell` API via contextBridge
  - Renderer (`src/renderer/`) adapted from `docs/ui-design/` with live WebSocket client
  - Dev scripts: `pnpm --filter @nusashell/desktop run dev`
- `installPath` field added to `PluginView`, `PluginListItem`, `PluginDto`, and `PluginGetResultDto` so the renderer can locate plugin UI files

### Changed
- `PluginListItemSchema` now includes `installPath` (Zod schema updated)
- All test fixtures updated to include `installPath` in mock plugin data

## [0.0.14] - 2026-07-28

### Added
- `icon` field on `PluginDto`, `PluginListItemSchema`, `PluginView`, `PluginListItem`, and `GetPluginResult` — plugin icons now flow from manifest through the application layer to the wire protocol and frontend
- `resolveIcon()` helper in application layer — resolves `file://icon.png` (relative to plugin dir) and `./icon.png` to absolute `file:///` URLs using `plugin.installPath`; passes through `http(s)://` URLs, absolute `file:///` paths, and text/emoji as-is
- Manifest schema doc comment documenting three accepted icon formats: text/emoji, file path (`file://relative.png` or `file:///abs/path`), and URL (`http(s)://`)
- Manifest schema tests for file path, URL, and relative path icon formats
- Unit tests for `resolveIcon()` (9 tests covering all format types)
- E2E test assertions verifying `icon` is present in `plugin.list` and `plugin.get` responses
- UI mockup `renderIconHtml()` helper that renders `<img>` for URL/file icons and text for emoji/letter icons, with `onerror` fallback for broken file paths
- Mock "Tasks" plugin with URL icon and "Timer" plugin with `file://` relative icon to demonstrate all three icon formats in the UI

### Changed
- `PluginRuntimeManager` resolves `icon` via `resolveIcon(plugin.manifest.icon, plugin.installPath)` in `listPlugins`, `getPlugin`, and `startLocked`
- `ListPluginsHandler` maps `icon` from `PluginView` to `PluginListItem`
- All test mocks for plugin list items updated with `icon` field
- UI mockup rendering functions (app grid, installed table, running list, drawer, plugin window) use `renderIconHtml()` instead of hardcoded emoji spans

## [0.0.13] - 2026-07-28

### Added
- `NusaClient.subscribe()` and `NusaClient.unsubscribe()` convenience methods with typed `EventType[]` parameter
- Auto-resubscribe: `NusaClient` tracks active subscriptions and re-sends them to the server after reconnect
- Reconnect integration test verifying server-side subscription registry is populated after auto-resubscribe

### Changed
- E2E test uses `client.subscribe()` instead of raw `client.request("subscribe", ...)`
- `NusaClient` clears `activeSubscriptions` on disconnect, intentional close, and reconnect exhaustion

## [0.0.12] - 2026-07-28

### Added
- Event `sequence` field: monotonic counter in `WebSocketEventPublisher`, `sequence` on `EventEnvelope` and all Zod event schemas, `lastSequence` tracking in `EventSubscriber`
- Client subscription registry: per-session event filtering via `ClientSubscriptionRegistry`, `subscribe`/`unsubscribe` request methods and schemas, opt-in model (sessions receive no events until subscribed)
- Protocol version negotiation: `PROTOCOL_VERSION` constant, `protocolVersion` optional field on `RequestEnvelope` and all request schemas, server-side `UNSUPPORTED_VERSION` rejection, `NusaClient` sends `protocolVersion: "1.0"` on all requests
- Unit tests for `ClientSubscriptionRegistry` (9 tests)
- WebSocket server tests for unsupported and supported protocol version negotiation

### Changed
- `WebSocketEventPublisher` constructor accepts optional `ClientSubscriptionRegistry` for filtering
- `WebSocketServer` intercepts `subscribe`/`unsubscribe` messages before routing, clears subscriptions on disconnect
- E2E test subscribes to all events before expecting event delivery

### Removed
- `packages/shared` stub package (unused `@nusashell/shared`)

## [0.0.11] - 2026-07-28

### Added
- `UNAVAILABLE` and `UNAUTHORIZED` error codes in `ApplicationErrorCode` + `ERROR_CODE_MAP`
- `LoggerPort` interface in `@nusashell/application` for infra-agnostic logging
- `HttpMcpClient` adapter using `StreamableHTTPClientTransport` from MCP SDK
- `SseMcpClient` adapter using `SSEClientTransport` from MCP SDK
- Testing fixtures: `manifestFixture()`, `manifestFixtureWith()`, `pluginFixture()`, `runningPluginFixture()` in `@nusashell/testing`

### Changed
- `NodeChildProcessAdapter` accepts optional `Logger` — logs spawn/debug/error
- `McpClientFactory` accepts optional `Logger` — passes to all transport adapters
- `StdioMcpClient` accepts optional `Logger` — logs connect/close/onClose, catches close errors
- `FilesystemPluginRegistry` accepts optional `Logger` — replaces silent `catch {}` with `logger.warn`
- `PluginRuntimeManagerDeps` accepts optional `logger?: LoggerPort` — replaces silent `catch {}` on MCP client close with `logger.warn`
- `ShutdownCoordinator` replaces `catch {}` with `container.logger.warn`
- Container wires `logger` to `NodeChildProcessAdapter`, `McpClientFactory`, `FilesystemPluginRegistry`, `PluginRuntimeManager`
- `McpClientFactory.createForHttp` / `createForSse` no longer throw — return real adapter instances
- Error mapper test covers `UNAUTHORIZED` and `UNAVAILABLE` codes
- 204 tests pass across 32 test files

## [0.0.10] - 2026-07-27

### Added
- `@nusashell/testing` shared test infra package with fakes (FakeClock, FakeMcpClient, FakeProcessAdapter, FakePluginRepository) and helpers (WebSocketTestClient, eventually)
- Client disconnect during active request race-condition test (§15)
- Shutdown coordinator completion: reject new commands via `MessageRouter.close()`, close active sessions, close DB
- Config loading from env vars (`NUSASHELL_PORT`, `NUSASHELL_HOST`, `NUSASHELL_PLUGINS_ROOT`, `NUSASHELL_DB_PATH`, `NUSASHELL_LOG_LEVEL`)
- Pino logging infrastructure (`createLogger` in `@nusashell/infrastructure`)
- `system.ping` and `system.version` query handlers + `SystemApi` in plugin-sdk
- `system.ping` / `system.version` request schemas in contracts
- Build step with tsup for all publishable packages (domain, contracts, application, infrastructure, transport-ws, plugin-sdk)
- `tsconfig.build.json` per package for DTS generation

### Changed
- `MessageRouter` now has `close()` method and `isClosed` flag to reject requests during shutdown
- `ShutdownCoordinator` follows full §14 sequence: stop WS → reject commands → close sessions → stop runtimes → close DB
- `bootstrap()` now loads config from env vars via `loadConfig()` and accepts partial overrides
- `createContainer` accepts `logLevel` option and exposes `logger` on the container
- Application `tests/fakes.ts` now re-exports from `@nusashell/testing`

## [0.0.9] - 2026-07-27

### Added

- **Reconnect policy in plugin-sdk**: `ReconnectPolicy` class with exponential backoff + jitter
  - Configurable: `enabled`, `maxAttempts`, `initialDelayMs`, `maxDelayMs`, `backoffFactor`, `jitterMs`
  - `shouldRetry()`, `getDelay()`, `recordAttempt()`, `reset()`, `isExhausted`, `state` getter
- **`NusaClient` auto-reconnect**: on unexpected WebSocket close, client schedules reconnect with backoff
  - Event handlers preserved across reconnect (implicit resubscribe)
  - Pending requests rejected on disconnect (stale); new requests work after reconnect
  - `onReconnect(callback)` and `onReconnectFailed(callback)` hooks for UI status indicators
  - `isReconnecting` getter
  - Explicit `disconnect()` skips reconnect entirely
- 11 `ReconnectPolicy` unit tests + 6 reconnect integration tests (server kill/restart, handler preservation, callback firing, maxAttempts exhaustion, explicit disconnect, pending request rejection + recovery)

### Changed

- `NusaClientOptions` now accepts `reconnect?: Partial<ReconnectOptions>`
- `NusaClient.onClose` no longer clears event handlers on auto-reconnect (only on explicit disconnect or exhaustion)
- Exported `ReconnectPolicy`, `ReconnectOptions`, `ReconnectState`, `DEFAULT_RECONNECT_OPTIONS`, `ReconnectStatusCallback` from `@nusashell/plugin-sdk`
- 188 tests pass across 27 test files

## [0.0.8] - 2026-07-27

### Added

- **Manifest schema validation (Zod)**: `ManifestSchema` in `@nusashell/contracts` validates manifest.json shape (id, name, version, icon, ui, mcp, dependencies) with Zod
- **`validate-manifest` CLI script**: `pnpm --filter @nusashell/infrastructure validate-manifest <path>` — validates a single manifest.json or scans a plugins root directory
- **SQLite persistence**: `SqliteDatabase` + `SqlitePluginRepository` implementing `PluginRepositoryPort` with `better-sqlite3`
  - Migration system (`001-init.sql`) with `schema_migrations` tracking table
  - WAL journal mode, UPSERT on save, full manifest serialization/deserialization
  - Container wiring: `dbPath` option in `ContainerOptions` selects SQLite; falls back to filesystem or in-memory
- **Race-condition tests (§15)**: 8 new tests in `plugin-runtime-manager.race.test.ts`
  - Concurrent start + stop (both orderings)
  - callTool while starting
  - Timeout followed by late response
  - Backend shutdown while plugins active (stopAll + pending call cancellation)
  - Duplicate request ID (no deadlock)
  - Concurrent restart + stop
- 11 manifest schema tests, 7 SQLite repository tests

### Changed

- `FilesystemPluginRegistry` now uses `ManifestSchema.safeParse()` instead of raw `JSON.parse` + `as RawManifest`
- `@nusashell/infrastructure` depends on `@nusashell/contracts`
- `pnpm-workspace.yaml`: `allowBuilds` for `better-sqlite3` and `esbuild` set to `true`
- Container `pluginRepository` type changed from union to `PluginRepositoryPort`
- 171 tests pass across 25 test files

## [0.0.7] - 2026-07-27

### Added

- `tool.cancel` command — cancel a pending tool call by `requestId` (`CancelToolCallHandler`)
- `tool.list` query — list MCP tools from a running plugin (`ListToolsHandler`)
- `plugin.restart` command — stop then start a plugin in one operation (`RestartPluginHandler`)
- `plugin.get` query — get single plugin details by ID (`GetPluginHandler`)
- `plugin.state` query — get just the runtime state of a plugin (`GetPluginStateHandler`)
- `PluginRuntimeManager.cancelTool()`, `.listTools()`, `.restartPlugin()`, `.getPlugin()` public methods
- `PLUGIN_NOT_RUNNING` error code for tool operations on non-running plugins
- `PluginGetResultDto`, `ToolListResultDto` contract types
- `PluginsApi.restart()`, `.get()`, `.getState()` and `ToolsApi.list()` in plugin-sdk
- 5 new E2E tests: get-plugin, get-plugin-state, restart, list-tools, tool.list-when-not-running

### Changed

- `RequestMethod` type extended with `plugin.restart`, `plugin.get`, `plugin.state`, `tool.list`
- Request schemas: added `PluginRestartRequestSchema`, `PluginGetRequestSchema`, `PluginStateRequestSchema`, `ToolListRequestSchema`
- `command.mapper.ts`: handles `plugin.restart` and `tool.cancel`
- `query.mapper.ts`: handles `plugin.get`, `plugin.state`, `tool.list`
- `error.mapper.ts`: maps `PLUGIN_NOT_RUNNING` error code
- Container registers all new handlers in command/query buses
- 145 tests pass across 22 test files (11 E2E)

## [0.0.6] - 2026-07-27

### Added

- `plugins/notes/`: built-in notes plugin using official MCP SDK
  - `manifest.json` with `command: "node", args: ["mcp/server.js"]`
  - `mcp/server.js`: MCP server with `createNote` and `listNotes` tools (in-memory)
  - `ui/index.html`: placeholder UI
- `apps/backend/tests/e2e.test.ts`: 6 end-to-end integration tests
  - Connect NusaClient → list plugins → start plugin → receive `plugin.started` event → call `createNote` → call `listNotes` → stop plugin
  - Uses real `FilesystemPluginRegistry`, real MCP server process, real WebSocket transport

### Changed

- `PluginManifest`: added `mcp.args?: readonly string[]` for command arguments
- `PluginRuntimeManager`: passes `manifest.mcp.args` and `plugin.installPath` (as `cwd`) to MCP client factory
- `PluginRuntimeManager`: removed double-spawn for stdio (MCP client owns the process via `StdioClientTransport`)
- `PluginRuntimeManager`: crash detection for stdio via `McpClientPort.onClose` callback instead of `ProcessHandle.exited`
- `McpClientPort`: added optional `onClose` callback and `pid` getter
- `StdioMcpClient`: implements `onClose` (via `StdioClientTransport.onclose`) and `pid` getter; accepts `cwd` parameter
- `PluginView`: enriched with `name`, `version`, `enabled` from manifest
- `ListPluginsHandler`: returns actual plugin name/version/enabled from manifest instead of placeholder values
- `pnpm-workspace.yaml`: includes `plugins/*`

### Notes

- 140 tests pass across 8 packages (22 test files).
- MVP is now runnable end-to-end: backend → WebSocket → plugin lifecycle → MCP tool calls.

## [0.0.5] - 2026-07-27

### Added

- `packages/plugin-sdk`: `NusaClient` WebSocket client for renderers and hosts
  - `RequestManager` — request/response correlation by `id` with timeout and connection-closed rejection
  - `EventSubscriber` — typed event subscriptions (`plugin.started`, `plugin.stopped`, etc.)
  - `WebSocketConnection` — thin `ws` wrapper with connect/disconnect/status
  - `NusaClient` — main client: `connect()`, `disconnect()`, `plugins.start/stop/list()`, `tools.call/cancel()`, `events.on()`
  - `PluginsApi` + `ToolsApi` facades
  - Error classes: `NusaClientError`, `RequestTimeoutError`, `ConnectionClosedError`
  - 11 plugin-sdk tests (request manager unit + NusaClient integration with live WS server)
- `apps/backend`: composition root wiring all layers
  - `createContainer()` — manual DI: SystemClock, FilesystemPluginRegistry/InMemoryPluginRepository, NodeChildProcessAdapter, McpClientFactory, EventDispatcher, PluginRuntimeManager, CommandBus, QueryBus, MessageRouter, WebSocketServer, WebSocketEventPublisher
  - `bootstrap()` — starts WS server, wires SIGTERM/SIGINT to shutdown
  - `ShutdownCoordinator` — stops WS server, stops all plugin runtimes, exits
  - 3 backend tests (container wiring, WS connection, plugin.list query)
- `tsx` dev dependency for running backend directly from TypeScript
- Plugin-sdk + backend added to `vitest.workspace.ts`

### Notes

- No SQLite for MVP — `FilesystemPluginRegistry` used (per backend-structure.md §18).
- No Pino logger yet — console-based (swap later).
- No auth — `websocket-authenticator` deferred.
- 134 tests pass across 7 packages (domain, application, infrastructure, contracts, transport-ws, plugin-sdk, backend).

## [0.0.4] - 2026-07-27

### Added

- `packages/contracts`: WebSocket protocol DTOs + Zod schemas
  - Request/response/event message types with discriminated unions
  - Zod schemas for `plugin.start`, `plugin.stop`, `plugin.list`, `tool.call`, `tool.cancel`
  - Event schemas for `plugin.started`, `plugin.stopped`, `plugin.crashed`, `plugin.state_changed`, `tool.call_completed`
  - Plugin and tool DTOs
  - 25 contract tests
- `packages/transport-ws`: WebSocket transport layer
  - `ProtocolError` + `validateIncomingMessage` — Zod-based request validation
  - Mappers: command, query, response, error, client-event
  - `MessageRouter` — routes validated requests to command/query bus
  - `WebSocketSession` + `SessionRegistry` — connection lifecycle
  - `WebSocketServer` — `ws`-based server accepting connections and dispatching messages
  - `WebSocketEventPublisher` — broadcasts domain events to all sessions
  - 26 transport tests (validator, mappers, router, server integration)
- `zod` and `ws` dependencies
- Contracts + transport-ws added to `vitest.workspace.ts`

### Notes

- Auth deferred for MVP — no `websocket-authenticator` in this phase.
- Bootstrap (apps/backend composition root) deferred to next phase.

## [0.0.3] - 2026-07-27

### Added

- `packages/infrastructure`: concrete adapters for application ports
  - `SystemClock` — implements `ClockPort` using `new Date()`
  - `InMemoryPluginRepository` — implements `PluginRepositoryPort` for tests/early spike
  - `NodeChildProcessAdapter` — implements `PluginProcessPort` using `child_process.spawn`
  - `StdioMcpClient` + `McpClientFactory` — implements `McpClientPort`/`McpClientFactoryPort` using official MCP TypeScript SDK over stdio transport
  - `FilesystemPluginRegistry` — implements `PluginRepositoryPort` scanning plugin directories for `manifest.json`
  - `plugin-directory-layout` — helpers for scanning and resolving plugin paths
- 19 infrastructure tests (system clock, in-memory repo, child process, MCP stdio client, filesystem registry)
- `@modelcontextprotocol/sdk` dependency
- Infrastructure added to `vitest.workspace.ts`

### Notes

- HTTP and SSE MCP transports are stubs (stdio only for MVP).
- SQLite deferred — filesystem/JSON registry is acceptable for MVP per `docs/backend-structure.md` §18.

## [0.0.2] - 2026-07-27

### Added

- pnpm monorepo scaffold: `apps/` (backend, desktop stubs) and `packages/` (application, infrastructure, transport-ws, contracts, plugin-sdk, shared, testing stubs)
- `packages/domain`: pure domain layer — plugin/tool entities, value objects, lifecycle policies, domain events, errors, and `Result` primitive
- Vitest unit tests for runtime transition matrix and plugin lifecycle rules
- Workspace tooling: `tsconfig.base.json` (strict), `vitest.workspace.ts`, root `typecheck` / `test` scripts

### Notes

- `packages/domain` is the first implemented package; other packages are stubs pending application/infrastructure work.
- PoC under `docs/PoC/` remains the runnable behavioral reference.

## [0.0.1] - 2026-07-27

### Added

- Concept-stage product docs: `README.md`, `docs/blueprint.md`, `docs/backend-structure.md`
- Runnable zero-dep bridge PoC under `docs/PoC/` (launcher + Notes plugin + stdio MCP)
- Launcher visual sketch under `docs/ui-design/`
- Agent guidance: root `AGENTS.md`
- Project skill: `.agents/skills/frontend-design/`
- Versioning scaffold: `VERSION`, this changelog, `.github/pull_request_template.md`

### Notes

- No `apps/` or `packages/` monorepo yet - target layout is specified in
  `docs/backend-structure.md` and is the next build milestone.
- Docs language is English throughout.
