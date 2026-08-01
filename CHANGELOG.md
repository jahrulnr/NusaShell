# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.1] - 2026-08-02

### Fixed

- **Linux curl installer no longer claims success when Chromium sandbox is broken.**
  Chromium aborts if `chrome-sandbox` exists without root ownership and mode
  `4755`, even when unprivileged user namespaces are on. The installer now
  detects that before finishing, prompts for a one-time `sudo` fix (via
  `/dev/tty` so `curl | bash` still prompts), and falls back to renaming the
  helper + `--no-sandbox` when sudo is declined or unavailable.

## [0.1.0] - 2026-08-02

### Added

- User-space Linux/macOS and Windows installers with SHA-256 verification.
- Installation and update-channel documentation, plus curl-based README quickstart.

## [0.0.58] - 2026-08-01

### Fixed

- **Windows Electron packaging now has the required author metadata.**
  `apps/desktop/package.json` now declares a non-empty `author`, preventing
  Electron Packager from aborting before it creates the Windows distributable.
- **MCP Roots `file://` URIs now round-trip correctly on Windows.**
  `mcp-session-manager.ts` `applyRoots` built URIs via
  `` `file://${workspace}` `` string concatenation — on Windows this produces
  `file://C:\Users\...` which is not a valid file URI. The Files plugin parsed
  it back with `.replace(/^file:\/\//, "")`, losing the drive letter. Both sides
  now use `pathToFileURL(path.resolve(workspace)).href` (build) and
  `fileURLToPath(uri)` (parse). Rebuilt `plugins/files/mcp/server.cjs`.
- **Backend composer defaults no longer use `new URL(...).pathname`.** On
  Windows, `.pathname` returns a leading-slash path without the drive letter
  (e.g. `/C:/Users/...`). All four composers (`plugin-runtime.ts`,
  `agent-runtime.ts`, `job-runtime.ts`, `skills-runtime.ts`) now use
  `fileURLToPath(new URL(rel, import.meta.url))`.
- **Desktop durable state always lives under Electron `userData`.**
  `getDataRoot()` previously returned the repo root when unpackaged, mixing
  durable state (docs-index cache, skills, memory, settings) into the git
  checkout. It now always returns `app.getPath("userData")` — packaged and
  unpackaged. Bundled read-only assets (prompts, docs, plugins) still resolve
  from the app/repo tree via `getRuntimeRoot()`.
- **Workspace label handles Windows backslash paths.** `updateWorkspaceLabel`
  used `ws.split("/").pop()` which breaks on `D:\proj`. Now splits on
  `[\\/]/` to get the basename on both POSIX and Windows.

### Added

- **ADR: cross-platform path layout** (`docs/architecture/path-layout.md`).
  Documents the stateRoot vs tmp vs bundle placement policy and the
  `pathToFileURL` / `fileURLToPath` rules for `file://` round-trips.

### Tests

- `plugin-runtime-manager.workspace.test.ts` now computes expected `file://`
  URIs via `pathToFileURL(resolve(...))` — the same call `applyRoots` uses — so
  tests pass on both POSIX and Windows.
- `files-bundle-sandbox.test.ts` replaced `os.tmpdir() + "/prefix"` with
  `join(os.tmpdir(), "prefix")` for cross-platform path construction.
- `window-assets.test.ts` now computes expected paths via `resolve` / `join`
  (same calls as the implementation) instead of hardcoding POSIX strings.
- `app-behavior-settings.test.ts` skips the `0o600` permission-bit assertion on
  Windows — NTFS ACLs don't map to POSIX mode bits, so `stat().mode & 0o777`
  returns a different value than the requested `0o600`.

## [0.0.57] - 2026-08-01

### Fixed

- **Windows CI no longer fails on `better-sqlite3` native build.** The
  `windows-latest` GitHub Actions runner ships Visual Studio 18, which
  `@electron/node-gyp` (used by `electron-rebuild`) does not recognize — it
  reports `unknown version "undefined"` and aborts. The desktop postinstall used
  `electron-rebuild -f -w better-sqlite3` where `-f` forced a from-source
  rebuild even though `better-sqlite3@13.0.1` ships N-API prebuilds for
  `win32-x64` (ABI-stable across Node and Electron). Removing the `-f` flag lets
  `electron-rebuild` detect the prebuild as compatible and skip the rebuild.
  CI test jobs use `pnpm install --ignore-scripts` to skip postinstall scripts
  entirely. Frontend jobs then run `node node_modules/electron/install.js`
  explicitly to fetch the Electron binary needed by tray tests (without
  triggering `electron-rebuild`). Backend jobs need no extra step — N-API
  prebuilds work under system Node.
- **Windows CI: path separator failures in backend tests.** Five test files
  hardcoded POSIX paths or used `path.posix` in expectations, producing
  `D:\tmp\proj` vs `/tmp/proj` mismatches on Windows. Fixed by switching to
  platform-aware `path.resolve` / `path.join` and computing expected `file://`
  URLs via `pathToFileURL(resolve(...))` — the same calls the implementation
  uses. Affected: `icon-resolver.test.ts`, `workspace-tool-wrap.test.ts`,
  `mcp-agent-tool-gateway.test.ts`, `plugin-path-checks.test.ts`,
  `fs-service.test.js`.
- **Windows CI: `.mjs` SyntaxError from CRLF line endings.** Git's
  `core.autocrlf=true` on Windows converts LF→CRLF on checkout, breaking
  shebangs and ESM imports in `.mjs` files (`scan-ui-docs.mjs` imported by
  `markdown-docs-index.test.ts`). Added `.gitattributes` with
  `* text=auto eol=lf` to force LF for all text files across platforms.

## [0.0.56] - 2026-08-01

### Added

- **Cross-platform CI:** GitHub Actions now runs frontend and backend tests in
  parallel on Linux, Windows, and macOS with Node.js 24, then builds native
  Electron distributables on all three platforms. Build artifacts are retained
  for 14 days; the final GitHub Release and version-tag job is scaffolded but
  intentionally disabled. When enabled, it reads the tag from `VERSION` and
  publishes the matching `CHANGELOG.md` section as the GitHub Release notes.

### Fixed

- Workspace declaration builds and backend startup no longer fail on stale
  imports left behind by runtime-manager and agent-service extractions. AI
  strategy bindings, persisted job defaults, and optional job caller context
  now also satisfy their declared contracts.
- **ACP model label no longer sticks on regular chats after switching.** The
  shared `#agent-model-trigger-label` was written as `"{model} · ACP"` by
  `updateAcpModelLabel` after `ensureAcpSession` / `refreshAcpConfigOptions` /
  `selectAcpConfigOption` awaits resolved — without checking the conversation
  was still ACP. Switching to a regular chat called `refreshModelPicker()`
  (correct label), then a late ACP resolve overwrote it again. All three async
  ACP methods now capture the conversation id before the await and re-check
  `kind === "acp"` + `activeId === startedId` after, via the new
  `shouldApplyAcpUiUpdate` helper. `updateAcpModelLabel` also early-returns
  unless the current conversation is ACP. The leave-ACP path (hide chrome, clear
  `acpConfigOptions`, `refreshModelPicker()`) is unchanged and stays synchronous.

### Tests

- Added `shouldApplyAcpUiUpdate({ activeId, activeKind, startedId })` unit tests
  covering: same ACP conversation (true), switched to regular chat (false),
  switched to a different conversation (false), missing ids (false), and
  switched to a different ACP conversation (false).

## [0.0.55] - 2026-08-01

### Fixed

- **Agent context badge no longer inflates after multi-round tool turns.** The
  composer status badge is meant to show approximate *current prompt window*
  fill, but it mixed local `chars/4` estimates with cumulative
  `usage.inputTokens` summed across tool rounds (a billing total) and took
  `Math.max`, so a 12k-window turn with N tool rounds displayed ~N× the real
  window. The badge now uses `agent.context` `estimatedTokens` (window estimate)
  only and ignores cumulative `inputTokens` on context events. After a turn,
  `refresh()` re-estimates from persisted messages and the result is no longer
  overwritten with `result.usage.inputTokens`.
- **Idle badge no longer ~2× for assistant messages with `steps`.**
  `estimateMessageChars` double-counted `content` + `reasoning` + `toolCalls`
  and `steps` (which mirror them in durable assistant messages). When `steps` is
  a non-empty array, the estimator now uses `steps` only (plus `attachments` for
  user messages); it falls back to `content`/`reasoning`/`toolCalls` only when
  `steps` are absent.
- **ACP turns now refresh the context badge.** `submitAcp` never updated the
  badge during/after ACP streams; it now calls `updateContextStatus()` in
  `finally` so the badge reflects the current window fill after an ACP turn.

### Changed

- `resolveContextBadgeTokens` now accepts an optional `inputTokens` field in its
  input (cumulative billing — intentionally ignored) so callers can pass the full
  `agent.context` event payload without a separate strip step. The controller's
  `onContextUpdate` passes `inputTokens` through; the helper drops it.

### Tests

- Added BH-CTX-01..09 bug-hunt catalog tests (1:1 named) covering: multi-round
  tool turns not summing prompts (01), post-turn badge matching reopen (02),
  steps/body not double-counted (03), window estimate preferred over cumulative
  usage (04), unknown/known window formats (05/06), empty thread (07), ACP
  refresh (08), and hostile transcript text treated as opaque length (09).

## [0.0.54] - 2026-08-01

### Added

- **Headless MCP plugins (optional `ui`)** — a plugin manifest may now omit `ui`
  to ship a headless MCP-only plugin. Headless plugins never open a
  `BrowserWindow`, do not appear on the Home launcher grid, and are managed from
  the Plugins view (Start / Stop / Autostart / uninstall) and via agent `mcp_*`
  tools. `icon` stays required (emoji/text is valid). Existing manifests with
  `ui` are unchanged (backward compatible).
  - **Contracts:** `ManifestSchema.ui`, `PluginListItemSchema.ui`, and
    `PluginDto.ui` are now optional. `ui.entry` must still be non-empty when
    `ui` is present.
  - **Domain:** `PluginManifestInput.ui` and the `PluginManifest.ui` field are
    optional; validation of `entry` and window mode only runs when `ui` is
    present; `toInput()` omits `ui` when absent.
  - **Application:** `RuntimeEntry.ui` / `PluginView.ui` are optional;
    `PluginRuntimeManager.ensureEntry` no longer invents a placeholder
    `ui: { entry: "ui/index.html" }`. `ListPluginsHandler` now forwards `ui`
    and `keepAliveOnClose` (previously stripped — see Fixed). New
    `hasPluginUi(view)` helper exported from `@nusashell/application`.
  - **Infrastructure:** `PluginInstaller.installFromDirectory` now `access()`es
    the resolved `ui.entry` file (when declared) and local file icons under the
    plugin folder before copy, failing the install early with
    `Invalid plugin package: ui.entry not found: …` / `icon not found: …` if
    missing or escaping the plugin dir. `PluginSyncService` applies the same
    check on cold sync and skips broken packages instead of resurrecting them.
    Shared util in `packages/infrastructure/src/plugins/plugin-path-checks.ts`.

### Changed

- **Desktop shell redesign:** replaced the blue dashboard styling with a
  graphite instrument-workbench system across Home, Agent, Skills, Learning,
  Plugins, AI Providers, Autostart, Logs, Jobs, Settings, drawers, and modals.
  The new shell uses normalized plugin launch plates, compact semantic tokens,
  phosphor state/action accents, visible keyboard focus, reduced-motion support,
  and responsive icon-only navigation at the 900px desktop minimum.
- Sidebar navigation and installed-plugin rows now use native buttons. The
  plugin details drawer is removed from the accessibility tree while closed,
  receives focus when opened, and restores focus to its trigger when dismissed.
- **Home launcher grid = UI plugins only.** Headless MCP-only plugins are
  filtered out of the Home grid (`hasPluginUi`); the Plugins view still lists
  all installed plugins. The Home empty state now distinguishes "no plugins
  installed" from "installed plugins are MCP-only".
- **Context menu `Open` is disabled for headless plugins** (no window to open).
  `openPluginWindow` in the renderer also guards against headless plugins as a
  defense-in-depth.

### Fixed

- Workbench theme polish now preserves natural plugin and brand icon colors,
  brings AI Provider cards and dialogs onto graphite theme surfaces, softens
  phosphor selection borders, and uses dark ink on filled phosphor controls for
  readable contrast.
- **`ListPluginsHandler` stripped `ui` and `keepAliveOnClose`** from the
  `plugin.list` response while `PluginListItemSchema` expected them, so the
  launcher could fall back to a phantom `ui/index.html` default via
  `normalizePluginWindowOptions` and open the wrong path for UI plugins whose
  entry differed. The handler now forwards both fields, and
  `normalizePluginWindowOptions` no longer silently defaults a missing entry to
  `ui/index.html` — it throws `Plugin has no UI entry (headless plugin);
  cannot open a window` so a headless plugin can never open a blank window.

## [0.0.53] - 2026-08-01

### Added

- **`job` agent meta-tool** — the foreground agent can now manage scheduled
  automation with full CRUD parity to the desktop Jobs surface and the
  `job.*` WS methods, through one always-on `job` meta-tool with an `action`
  enum (`list`, `validate_schedule`, `add`, `set_enabled`, `run`, `remove`,
  `output`). Same envelope style as `memory` (`{ ok, data?, error?, meta }`);
  `ApplicationError` codes `JOB_NOT_FOUND` / `JOB_INVALID_SCHEDULE` map to
  structured `job_not_found` / `job_invalid_schedule` envelopes so a bad
  action never crashes the turn. Reuses the existing schedule parser and
  `JobStorePort` / `JobScheduler` ports — no parallel model.
  - **Wiring:** job deps are late-bound because the agent is constructed
    before jobs in the composition root. `McpAgentToolGateway.bindJobs(store,
    scheduler)` is called in `apps/backend/src/container.ts` after
    `createJobRuntime(...)`; the `job` tool only appears in `listTools()` when
    both deps are bound.
  - **Anti-recursion:** `job` is added to `JobAgentToolGateway.JOB_DENYLIST`
    so a scheduled job turn cannot manage other jobs. The
    `ReviewAgentToolGateway` whitelist is unchanged — review stays
    learning-only (memory + skills), so `job` is also unavailable during
    background review turns.
  - Prompt copy states jobs run **only while NusaShell is open**, cron is
    UTC, and missed one-shots are not silently fired.
- `docs/architecture/job-automation.md` — new "Agent tools" section with the
  action table, envelope mapping, and wiring notes.
- `docs/architecture/progressive-mcp-tools.md` — cross-link to the `job` tool
  and `job-automation.md`.

### Changed

- `mcp-tools.md`, `developer.md`, and `system.md` prompts now document the
  `job` meta-tool and the app-open / UTC / no-silent-missed-one-shot
  semantics.

## [0.0.52] - 2026-08-01

### Fixed

- **ask_question multi-select + custom text dropped options** — when the user
  selected options AND typed custom text in a multi-select ask card, the
  submit logic picked `via: "text"` and silently dropped the selected
  `optionIds`. The UI explicitly allows both (multi-select doesn't clear the
  custom text field), but the submit + backend treated `via` as exclusive.
  Fixed: `submitAskCard` now sends both `optionIds` and `text` when both are
  present; `buildResult` combines them into `"LabelA, LabelB — custom text"`
  and exposes the supplementary `text` in the result `data`.
- **Streaming events never reached the renderer after the ws-client refactor**
  — the `subscribe(["*"])` call was issued before `connectWs()`, so
  `NusaClient.request()` rejected with "Not connected" and the subscription
  was silently swallowed by `.catch(() => {})`. The `NusaClient` `onOpen`
  callback only re-subscribes on reconnect, not on the initial connect, so
  the server never registered a subscription for the session and no
  `agent.text_delta` / `agent.reasoning_delta` / `agent.tool_call_start`
  / ask-card events were delivered. The agent appeared to hang on
  "Working…" with nothing streaming in (including `ask_question` cards).
  Fixed by moving `subscribe(["*"])` into the `onOpen` callback so it fires
  after the socket is open. Regression tests in
  `packages/plugin-sdk/tests/subscribe-after-connect.test.ts` verify that
  subscribe-before-connect rejects, subscribe-after-connect registers
  server-side, and initial connect does not auto-subscribe.

## [0.0.51] - 2026-08-01

### Added

- **Workspace MCP binding** — `conversation.workspace` is now the source of
  truth for agent tool I/O, not just prompt context. It flows through
  `RunAgentTurnHandler` → `AgentTurnContext.workspace` →
  `McpAgentToolGateway.beginTurn` and reaches bundled MCP tools in three
  locked layers (full design in
  `docs/architecture/workspace-mcp-binding.md`):
  - **Phase 1 — Gateway arg wrap:** the Terminal plugin's `cwd` defaults to
    the workspace when omitted/relative, and the Files plugin's relative
    `path`/`source`/`destination` arguments are rewritten to absolute paths
    under the workspace. Absolute paths and `/` are preserved; containment is
    still enforced by each plugin server. Third-party plugins are passed
    through unchanged.
  - **Phase 2 — MCP Roots:** the shell is now an MCP client that advertises
    the `roots` capability with `listChanged`. `StdioMcpClient` answers
    `roots/list` with the workspace root and sends `roots/list_changed` on
    change. The bundled Files server calls `roots/list` on connect and
    re-fetches on `roots/list_changed`, updating its in-process root via
    `FileService.setRoot` — no process restart.
  - **Phase 3 — Static-server respawn + enable overrides:** `mcp_enable`
    accepts optional `args`/`env` overrides (command is immutable); a
    different launchSpec while running triggers a stop+start respawn. Static
    servers get `NUSASHELL_WORKSPACE` at spawn. `mcp_list` now enriches
    plugins with a redacted `launchSpec` (command, args, env keys — values
    redacted, roots capability flag).
- `docs/RISK.md` — residual risk register (agent MCP launch overrides /
  `npx` argument swap, advisory roots, workspace binding scope).
- `docs/architecture/workspace-mcp-binding.md` — full ADR for the wrap →
  Roots → respawn binding order and the rejected-for-MVP alternatives.
- Files plugin: `NUSASHELL_WORKSPACE` env fallback (after
  `NUSASHELL_FILES_ROOT`) and `FileService.setRoot` for in-process root
  updates from MCP Roots.

### Changed

- `AgentTurnContext` now carries an optional `workspace` field; the gateway
  captures it per turn and syncs it to roots-capable plugins before granted
  tool calls.
- `McpClientPort` adds optional `setRoots`, `notifyRootsChanged`, and
  `rootsRequested` members; `PluginRuntimeManager` adds `syncWorkspace` and
  `getLaunchSpec`; `StartPluginOptions` carries optional `args`/`env`/
  `workspace` overrides.
- `mcp-capability-policy.md` moves Roots from "deferred" to "implemented
  now" with the interoperability-not-security caveat.
- Developer and MCP-tools prompts now describe the workspace as bound to
  bundled tools (was: "prompt context only, pass absolute paths yourself").

## [0.0.50] - 2026-08-01

### Fixed

- **Files sandbox bundle escape (P0, critical)** — the shipped Files MCP
  bundle (`plugins/files/mcp/server.cjs`) was stale and returned resolved
  paths with no containment check, so `../../` traversal and absolute paths
  outside `NUSASHELL_FILES_ROOT` escaped the sandbox. Rebuilt the bundle so
  the guarded `resolvePath` lives in the shipped artifact, and added a
  bundle-containment regression test plus a runtime stdio MCP sandbox test
  that spawns the bundle and verifies escape is rejected.
- **Plugin crash status SoT (P1)** — killing an MCP process from outside
  NusaShell left the runtime state stuck on `running` because the close
  watcher was registered after the `running` transition and `plugin.started`
  event. The watcher is now registered before the transition (closing the
  race window), the `onClose` path catches deaths during `starting` too, and
  `plugin.started` carries the real `pid` instead of a hardcoded `0`.
- **Tools=0 honesty (P2)** — the launcher's `listTools` swallowed `tool.list`
  errors as an empty tool list, so the plugin drawer showed "No tools
  available" even when the listing failed. The drawer now distinguishes a
  failed listing ("Tools unavailable: …") from a genuine empty toolset, via a
  tested `describeToolsPanel` helper.

### Added

- `docs/architecture/plugin-sandbox-readiness.md` documenting the three
  mitigations and the deferred finding 3b (`ui.capture` / `panelIndex` /
  `FileSystem not renderable` — not found in this repo).
- `plugins/files/README.md` documenting the bundle rebuild + containment
  contract.

## [0.0.49] - 2026-08-01

### Added

- **Codex ACP provider** — OpenAI Codex is now a first-class ACP provider
  alongside Cursor. The manifest seeds `NO_BROWSER=1` and
  `INITIAL_AGENT_MODE=agent` spawn env defaults so the Codex CLI runs headless
  in agent mode. Authentication soft-fails: if `authenticate` errors (e.g.
  missing `CODEX_API_KEY`), the client logs a warning and proceeds to
  `session/new`, letting an existing `~/.codex` ChatGPT token drive the
  session. To use an API key instead, set `OPENAI_API_KEY`/`CODEX_API_KEY` in
  the Electron process env and choose `api-key` in Configure → Auth method.
- **ACP provider Connect button** — the AI Providers → ACP Agents card now
  shows a Connect button that runs a one-shot `acp.probe`
  (spawn → initialize → optional authenticate → session/new → close). The
  result is persisted as `authStatus` (`connected` | `needs-auth`),
  `authCheckedAt`, and `authError` on the provider config. The New ACP menu
  only lists providers whose `authStatus` is `connected`, so users cannot
  start a thread against an unauthenticated provider.
- **ACP provider extension model** — vendor-specific server→client requests
  are now dispatched to an `AcpProviderExtension` resolved per session.
  `CursorAcpExtension` owns `cursor/ask_question` and `cursor/create_plan`;
  `CodexAcpExtension` is a no-op placeholder for future Codex methods. This
  removes vendor branches from `AcpJsonRpcClient`.
- **ACP permission and ask cards** — `submitAcp` now wires
  `onPermissionRequest`/`onAskRequest` so ACP `session/request_permission`
  and `session/ask_question` render inline cards in the agent thread. The
  permission card has a distinct amber accent; answers flow back through
  `acp.permission_answer`/`acp.ask_answer`.
- **Codex manifest env merge** — provider `env` defaults are merged under
  `process.env` at spawn time (provider wins on conflict).

### Changed

- **`AcpProviderStore`** now persists `authMethodId`, `authStatus`,
  `authCheckedAt`, and `authError` alongside `enabled`/`command`/`args`.
  Status computation treats `authStatus === "connected"` as `configured`;
  otherwise enabled+detected providers show `needs-auth`.
- **Configure modal** adds an Auth method select listing the provider's
  advertised `authMethodIds`, plus a hint explaining Codex ChatGPT vs API key
  auth.

## [0.0.48] - 2026-08-01

### Added

- **Per-traceId stream sequencing** — agent and ACP streaming events now

### Fixed

- **Reasoning/thinking now streams live** — the SSE parser only recognized
  `reasoning_content`, `reasoning`, and `thinking` fields inside
  `choices[0].delta` as strings. Providers that send reasoning via
  separate SSE event types (`event: reasoning`), top-level fields without
  `choices`, array content blocks, or alternative field names
  (`reasoning_text`, `thinking_content`, `reasoning_details`) were silently
  dropped during streaming — reasoning only appeared after the turn
  completed. The parser now captures the `event:` field, handles arrays
  and objects in reasoning fields, checks 6 field name variants, and
  extracts top-level reasoning when no `choices` are present.
  carry a `streamSeq` integer (monotonic per `traceId`, starting at 1)
  assigned at the application publish site via `StreamSeqRegistry`. The WS
  transport copies it into the payload but does not generate it. The
  desktop renderer wraps handlers in a `createStreamSeqGate()` that drops
  stale events and flags gaps so the presenter can mark a turn incomplete.
- **Turn lifecycle events** — `agent.turn_started`, `agent.turn_end`
  (reason: completed / cancelled / failed / superseded),
  `agent.cancel_requested`, and `agent.turn_superseded` are now published
  on the WS event stream. `agent.cancel` returns `phase: "requested"`
  immediately; the UI waits for `agent.turn_end` (2-second fallback) before
  sealing streaming tool cards and the streaming message.
- **Supersede** — `agent.run` accepts `supersedeTraceId` to cancel an
  in-flight turn and emit `agent.turn_superseded` so the UI can mark the
  old turn as superseded.
- **Incomplete tool card sealing** — `tool_call_start` creates a skeleton
  card; if `turn_end` fires while any card is still running,
  `sealStreamingToolCardsIncomplete()` marks those cards as incomplete so
  the UI never leaves a spinning card behind.
- **WS-edge redaction** — tool call args, output, error strings, and
  `ApplicationError.details` are scrubbed at the WS mapper boundary before
  reaching the renderer. Secret-like keys (`password`, `token`, `apiKey`,
  …), Bearer tokens, `Authorization` headers, and `sk-` API keys are
  replaced with `[REDACTED]`.

### Fixed

- **Reasoning/thinking now streams live** — the SSE parser only recognized
  `reasoning_content`, `reasoning`, and `thinking` fields inside
  `choices[0].delta` as strings. Providers that send reasoning via
  separate SSE event types (`event: reasoning`), top-level fields without
  `choices`, array content blocks, or alternative field names
  (`reasoning_text`, `thinking_content`, `reasoning_details`) were silently
  dropped during streaming — reasoning only appeared after the turn
  completed. The parser now captures the `event:` field, handles arrays
  and objects in reasoning fields, checks 6 field name variants, and
  extracts top-level reasoning when no `choices` are present.

## [0.0.47] - 2026-08-01

### Added

- **Parallel tool rounds** — a provider round with multiple tool calls now
  executes them concurrently instead of serially.
  - `AgentTurnRunner` segments the batch into contiguous parallel-safe runs
    and standalone **barrier** segments. Barrier tools (`ask_question` for
    MVP) run alone, in order; non-barrier neighbors run through a bounded
    worker pool (`maxConcurrentToolCalls`, env
    `NUSASHELL_AI_MAX_CONCURRENT_TOOL_CALLS`, default 8, clamp 1–32).
    `maxConcurrentToolCalls: 1` is a full sequential escape hatch.
  - Same-plugin calls naturally serialize through the per-plugin
    `PluginOperationQueue` — "parallel" means cross-plugin / independent I/O
    overlap, not breaking plugin single-flight.
  - `onToolCallStart` fires for all calls in a segment up front (UI shows the
    full batch immediately). Results are collected in original call order
    regardless of completion order — every `tool_call_id` gets a tool result
    message (success, failure, or cancelled); siblings are never dropped.
  - On cancel mid-batch: in-flight calls drain via `cancelTurn` / MCP cancel;
    any slot without an execution is filled with a cancelled stub and
    `onToolCallEnd` is emitted so the UI seals every card.
  - Wired through `AiConfig`, `AgentRuntimeSettings`, `apps/backend`
    container + bootstrap, and desktop `main/index.ts`.
  - New unit tests: concurrent overlap (2 calls), order preservation despite
    reverse completion, barrier non-overlap (`ask_question`), cancel mid-batch
    with cancelled stubs, `maxConcurrentToolCalls: 1` sequential. Gateway
    tests: overlapping `activeCalls` on different plugins + `cancelTurn`,
    requestId unregister after settle.

## [0.0.46] - 2026-08-01

### Added

- **Mid-turn recover & continue** — a failed provider call no longer discards
  in-progress tool work for the turn.
  - `AgentTurnRunner` now performs a **soft recover** after the router/provider
    retry budget is exhausted: if the turn already accumulated tool results, it
    re-calls the provider with the same messages up to `softRecoverAttempts`
    times (default 1, max 3, env `NUSASHELL_AI_SOFT_RECOVER_ATTEMPTS`).
    Cancellation aborts immediately and is never retried.
  - When soft recover is exhausted with progress on the turn, the runner
    throws `AGENT_PROVIDER_FAILED` with a typed `details.partial` snapshot
    (`messages`, `steps`, `toolCalls`, `traceId`, `rounds`, optional
    `model`/`providerId`/`usage`).
  - New `agent.run` `resume?: boolean` flag (contracts + command mapper). When
    `resume: true`, `RunAgentTurnHandler` skips system-prompt injection so the
    provider sees the exact mid-turn context. `AgentRuntimeSettings` gains
    `softRecoverAttempts`; `AiConfig` and `apps/backend` container/bootstrap
    wire it through.
  - Desktop conversation contract gains `status?: "complete" | "interrupted"`
    and `resumeMessages?: unknown[]` on assistant messages. The store
    validates both, clamps `resumeMessages` at ~512 KiB (dropping it on
    overflow so Retry falls back to a full restart), and exposes
    `replaceLastInterrupted` + IPC `agent-conversations:replace-interrupted`.
  - On a mid-turn failure with `partial`, the desktop seals the streaming
    message, persists an **interrupted** assistant message carrying
    `resumeMessages`, and keeps the error footer + **Retry** button. **Retry**
    on an interrupted message calls `agent.run` with `resume: true` and the
    saved `resumeMessages`; on success the interrupted message is replaced
    with the completed assistant message.
  - `buildAgentContext` skips `status: "interrupted"` messages when building
    context for a new turn — interrupted progress lives only in
    `resumeMessages` for the continue path.
  - New unit tests: runner soft-recover + `details.partial` (4),
    `replaceLastInterrupted`/interrupted persistence/budget clamp (4),
    `buildAgentContext` skip (1), `agent.run` `resume` mapper (1).

## [0.0.45] - 2026-07-31

### Added

- **Agent Client Protocol (ACP) thread support** — first-class external-agent
  conversations that spawn a JSON-RPC stdio provider (e.g. Cursor `agent acp`)
  and stream text/thought/tool/plan/session events over the existing WebSocket.
  - New `packages/contracts` protocol: `acp.*` request/event methods + Zod
    schemas (`acp-request-schemas.ts`, `acp-event-schemas.ts`).
  - New `packages/application/src/acp/` domain area: `AcpSessionService`,
    `AcpPermissionService`, `AcpAskBridgeService`, commands (`run-acp-turn`,
    `cancel-acp-turn`, `answer-acp-permission`, `answer-acp-ask`),
    query (`get-acp-session-info`), and events (`acp.text_delta`,
    `acp.thought_delta`, `acp.tool_call`, `acp.tool_call_update`, `acp.plan`,
    `acp.permission_request`, `acp.ask_request`, `acp.session_state`,
    `acp.turn_end`).
  - `AcpJsonRpcClient` in `packages/infrastructure/src/acp/`: spawns the
    provider, handles JSON-RPC 2.0 framing, server→client `session/request_permission`,
    `cursor/ask_question`, `cursor/create_plan` requests, and forwards
    `session/update` notifications.
  - `transport-ws` command/query/event mapping and `apps/backend` container
    wiring for all ACP commands/queries and events.
  - Desktop ACP provider registry (`AcpProviderStore`, shared contract,
    preload API, main-process IPC, new ACP Agents section in AI Providers view).
  - Desktop conversation store supports `kind: "agent" | "acp"` and optional
    `acp` metadata; minimal ACP status bar, ACP thread button, and ACP pill
    controls in the Agent view.
  - New `ACP_SESSION_NOT_FOUND` and `ACP_PROVIDER_FAILED` error codes.

## [0.0.44] - 2026-07-31

### Added

- **Ask Question host meta-tool** — interactive `ask_question` pauses an
  agent turn mid-tool so the desktop can render a single/multi-select card
  (options + optional free text). The user's answer returns via
  `agent.ask_answer` and resumes the turn as the tool result.
  - `AskQuestionService` pending registry; tool listed only when
    `interactive: true` (desktop `agent.run`); denied on job/headless turns.
  - Stop/cancel rejects pending asks so the turn unblocks immediately.
  - Desktop conversation UI: interactive ask card, seal on answer, read-only
    card on conversation reload; `agent.run` WS timeout raised to 30 minutes.
  - Docs/prompts updated (`developer.md`, `mcp-tools.md`, progressive MCP
    tools); UI map + generated docs for ask-card controls.

## [0.0.43] - 2026-07-31

### Added

- **Job automation waist** — durable scheduled jobs that fire headless agent
  turns or plugin tool calls while NusaShell is open. Jobs support
  once/interval/cron schedules with grace rules, at-most-once dispatch,
  catchup for recurring jobs, and missed one-shot detection.
  - New `packages/application/src/job/` area: `Job` model, schedule parser
    (dependency-free 5-field cron matcher), `JobStorePort`,
    `JobAgentToolGateway` (denies memory/skill tools), `JobAgentExecutor`
    (headless `AgentTurnRunner` with inactivity watchdog), and
    `JobScheduler` (60s tick, `.tick.lock`, due selection, output
    persistence).
  - `SqliteJobStore` + `002-jobs.sql` migration; `JsonJobStore` fallback
    for dev environments without SQLite.
  - WS protocol: `job.add`, `job.list`, `job.set-enabled`, `job.run`,
    `job.remove`, `job.output`, `job.validate-schedule`.
  - Events: `job.completed`, `job.failed` mapped to client event envelopes.
  - Desktop **Jobs** sidebar view: list, create (modal with live schedule
    validation), run, pause/resume, output, remove. Toast notifications on
    job completion/failure.
  - Architecture doc: `docs/architecture/job-automation.md`.
  - Tests: 69 new tests covering schedule parsing, both store
    implementations, the restricted gateway, the headless executor, and the
    scheduler (at-most-once, missed one-shots, catchup, tick lock, error
    isolation).

### Changed

- Blueprint §4 documents the Jobs navigation item and surface.
- `backend-structure.md` references the job-automation architecture doc.
- Error mapper includes `JOB_NOT_FOUND` and `JOB_INVALID_SCHEDULE` codes.

## [0.0.42] - 2026-07-31

### Added

- System tray residency: closing the launcher can hide to the tray instead of
  quitting, so background review, skill curator, and future scheduled jobs keep
  running. Tray menu offers Open NusaShell and Quit; left-click toggles the
  window.
- OS login autostart (packaged builds): Linux writes XDG
  `~/.config/autostart/nusashell.desktop` (self-healed on each launch for
  AppImage path changes); macOS/Windows use `app.setLoginItemSettings`.
  Unpackaged/dev builds refuse login autostart with a clear error.
- `--hidden` / `--background` boot flag for tray-only login starts.
- Single-instance lock: a second launch focuses the existing window.
- Settings → **Startup & background** toggles: Launch at login, Start in tray,
  Keep running when window is closed. Persisted in `app-behavior.json`.

### Changed

- `window-all-closed` no longer quits when keep-in-background is enabled
  (non-darwin). Explicit Quit still runs full backend shutdown.
- Blueprint §4 documents tray-resident mode and distinguishes plugin MCP
  Autostart from OS login autostart.
- UI documentation regenerated from updated `ui-map.json` (16 docs).

## [0.0.41] - 2026-07-31

### Added

- Learning Journey view: visualizes the agent's acquired skills and memory
  entries as a timeline and constellation graph. Includes a time scrubber to
  reveal how the journey grew, a detail pane for inspecting and editing memory
  nodes, and node deletion (skill archive or memory removal) via IPC.
- `LearningGraphService` in the application layer: builds a graph of skill and
  memory nodes with edges, clusters, and stats; supports `getNode`, `editNode`,
  and `deleteNode` mutations with stale-node detection.
- `learning:*` IPC handlers and preload bridge (`shell.learning.graph`,
  `getNode`, `editNode`, `deleteNode`) connecting the renderer to the backend
  container.
- Skills view curator panel: shows curator status with Run and Dry-run buttons.
- Skills view pending writes panel: lists agent-authored skill writes awaiting
  approval with Approve/Reject buttons and a count badge.
- Skills view archived skills panel: lists curator-archived skills with Restore
  buttons and a count badge.
- Pin/unpin control in the Skills file editor: toggles skill pinned state to
  exclude it from curator archival.

### Changed

- Sidebar navigation now includes a Learning item between Skills and Plugins.
- `agent.learning_updated` events now refresh both the Learning view and the
  Skills pending/archived panels.
- UI documentation regenerated from updated `ui-map.json` (16 docs).

### Fixed

- Agent chat history no longer disappears after an Electron restart. Clamped
  tool outputs were persisted 2 chars over the store validator cap
  (`12_000 + "\n…"`), so the whole assistant message failed validation on load
  and was silently dropped. Producers now clamp inside the budget (output,
  args wrapper, reasoning/text steps), and the store repairs legacy over-cap
  fields on load instead of dropping the message.

## [0.0.39] - 2026-07-31

### Added

- `tool_schemas` batch meta-tool in the agent gateway: grants several tools from
  the same plugin in one call (`pluginId` + `toolNames[]`), returning `granted`
  and `missing` lists so agents save turns when they need multiple tools.

### Changed

- Files plugin tool descriptions and agent prompt/docs now state explicitly that
  Files `path` arguments are relative to the Files plugin root (user home by
  default, `NUSASHELL_FILES_ROOT`) and that `/` there is **not** the OS
  filesystem root. Agents are directed to the Terminal plugin with an absolute
  `cwd` for paths outside the Files root.

## [0.0.38] - 2026-07-31

### Added

- Terminal plugin (`nusashell.terminal`): real PTY-backed terminal MCP plus an
  interactive xterm.js UI. `terminal_exec` runs one-shot commands, while
  `terminal_open` / `terminal_write` / `terminal_read` / `terminal_resize` /
  `terminal_close` / `terminal_list` manage interactive sessions. Default
  working directory is the user's home directory when no absolute `cwd` is
  passed.

### Changed

- Agent prompts now state explicitly that the conversation workspace is prompt
  context only and is not injected into MCP tool arguments, environment
  variables, or plugin working directories. Tools that need a path or `cwd`
  must be given an explicit absolute path (`developer.md`, `mcp-tools.md`,
  plus matching docs and compaction notes).

## [0.0.37] - 2026-07-31

### Fixed

- Agent multi-round turns interleave stream segments in arrival order
  (reasoning / text / tool deltas), instead of forcing thinking → tool → reason
  or dumping every text segment into one bubble at the end. Persisted steps keep
  the same provider order per round.

- Context usage updates live during a turn via `agent.context` events (message
  estimate after each model/tool step, plus provider input tokens when available)
  instead of staying stuck on the pre-turn conversation size until completion.
  Opening a chat, returning to the agent view, or refreshing the model picker now
  recomputes from the active conversation instead of resetting to `0 ctx`.

- Reasoning markdown no longer auto-linkifies bare filenames like `plugins.md`
  (linkify treated `.md` as Moldova's TLD and rendered them as blue links).
  Explicit markdown links in thinking still use a muted amber instead of accent blue.

- Agent tool calls keep the timeline ›_ rail chrome and expand into nested
  `tool` / `Output` panels (args + truncated result), instead of a name-only row
  or a bordered terminal card.

- Markdown renderer now supports raw HTML tags like `<br>`, `<kbd>`, `<details>`,
  and `<img>` in agent messages. Previously `html: false` escaped all HTML
  literally, causing tags to leak as visible text. Enabled `html: true` in
  markdown-it and added `isomorphic-dompurify` sanitization with an explicit
  allowlist — `<script>`, `onerror`, and other dangerous markup is stripped.

### Changed

- `markdown-it` config now uses `linkify: true` and `breaks: true` for
  auto-linking URLs and converting newlines to `<br>`.

## [0.0.36] - 2026-07-31

### Added

- Agent limits and context compaction settings are now configurable from the
  settings page instead of being hardcoded. New "Agent limits" card exposes
  max tool rounds (default 50) and max repeated tool calls (default 50). New
  "Context compaction" card exposes compaction toggle (default on), max input
  tokens (default 12000), reserve tokens (default 3000), recent turns to keep
  (default 4), and summary max characters (default 12000). All values persist
  in AI settings and apply without restart.

### Changed

- Settings page design polished: card titles use uppercase letter-spacing
  with bottom borders, inputs have focus glow, cards have subtle gradient
  backgrounds, and mini buttons have active press feedback.

- `RunAgentTurnHandler` now accepts a mutable `AgentRuntimeSettings` reference
  instead of static `defaultMaxToolRounds` and `context` parameters. The
  container's `configureAiRuntime` updates the reference in place.

## [0.0.35] - 2026-07-31

### Added

- Per-conversation workspace picker: a folder button in the agent composer
  footer opens the OS directory dialog. The selected workspace is persisted
  per conversation and injected into the agent system prompt via `{{workspace}}`.
  Default is the user home directory when no workspace is selected.

- `addModel` now accepts an optional `contextWindow` so manually added models
  can report used/total context.

### Changed

- Context usage display: format is now `1k/1M context` (used/total) instead
  of raw token count. When the model has no known context window, shows
  `4k ctx` (formatted used tokens only).

## [0.0.34] - 2026-07-31

### Added

- Untrusted tool output wrapping: MCP tool results (prefix `mcp_`) are now
  wrapped in `<untrusted_tool_result>` delimiters before being fed back to the
  model. This is an architectural defense against indirect prompt injection
  from file contents, search results, or other external data — the model is
  told the content is data, not instructions. Embedded delimiter tokens are
  neutralized so attacker content cannot break out of the wrapper.

### Changed

- Agent UI streaming: markdown is now rendered progressively as deltas arrive,
  instead of showing raw markdown text during streaming and only rendering on
  completion.
- Agent system prompt now documents the `<untrusted_tool_result>` delimiter
  convention so the model understands the trust boundary.

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
