# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

- `plugins/examples/notes/`: example notes plugin using official MCP SDK
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
- `pnpm-workspace.yaml`: includes `plugins/examples/*`

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
