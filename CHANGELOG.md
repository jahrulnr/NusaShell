# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
