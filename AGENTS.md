# AGENTS.md - NusaShell

Instructions for humans and coding agents working in this repository.

## Project snapshot

NusaShell is a desktop-like **shell for AI tools**: each plugin bundles a **UI +
MCP server**; the shell brokers lifecycle and tool calls so plugins get a real
visual surface (not chat-only MCP).

**Current stage:** early scaffold. `packages/domain` is implemented; `apps/` and
other `packages/` are stubs. Authoritative intent lives in docs; the runnable
demo is `docs/PoC/`.

| Path | Role |
| --- | --- |
| `README.md` | Product intent, docs map, PoC quickstart |
| `packages/domain/` | Pure domain layer (plugin runtime, policies, events) |
| `docs/blueprint.md` | Product / plugin architecture, launcher UX, MCP transports |
| `docs/backend-structure.md` | Target Clean Architecture monorepo + WebSocket protocol |
| `docs/PoC/` | Behavioral bridge demo (not the target layout) |
| `docs/ui-design/` | Launcher visual sketch |
| `VERSION` | Current semver |
| `CHANGELOG.md` | User-facing notable changes |
| `.agents/skills/frontend-design/` | Distinctive UI design skill for shell/plugin surfaces |

## Start here (every task)

1. Read this file and the relevant section of `README.md`.
2. For product/plugin UX → `docs/blueprint.md`.
3. For backend folders, layers, WS protocol, MVP scope → `docs/backend-structure.md`.
4. Treat `docs/PoC/` as a **behavioral reference**, not the scaffold target.
5. For launcher / plugin UI work → also load `.agents/skills/frontend-design/SKILL.md`.

### Run the PoC

```bash
cd docs/PoC
node server.js
# open http://localhost:8420
```

No `npm install` required for the PoC.

## Architecture locks (do not violate)

- **Broker only:** plugin UI and MCP never peer-connect; all traffic goes through the shell.
- **Two transport layers (do not conflate):**
  - Plugin iframe ↔ host: `postMessage` / `window.shell.callTool`
  - Host ↔ backend: WebSocket (`ws`) via application commands/queries - **not** an internal event bus
- **Dependency rule:** `domain` must not import Electron, WebSocket, SQLite, `child_process`, filesystem, MCP SDK, HTTP, or SSE.
- **Runtime SoT:** live plugin runtime belongs to `PluginRuntimeManager` (memory). Installed metadata → SQLite (filesystem/JSON OK only as an early spike). Do not duplicate authoritative “running” state in the renderer, WS gateway, or DB.
- **Infrastructure must not** send WebSocket frames directly - publish domain/application events.
- **Security is deferred** until broker/lifecycle correctness is proven. Do not mix iframe sandboxing, install permissions, signing, or process isolation into the first plumbing milestone.
- **MVP stays slim:** no Redis, microservices, event sourcing, external CQRS frameworks, Socket.IO-in-core, or clustered workers for the first MVP.

Target stack (when scaffolding): Electron + TypeScript monorepo (pnpm), packages
`domain` / `application` / `infrastructure` / `transport-ws` / `contracts` /
`plugin-sdk`, Zod, official MCP TypeScript SDK, SQLite (`better-sqlite3`),
Vitest, Pino. Details: `docs/backend-structure.md` §2 / §18 / §19.

## Plugin contract

A plugin is one folder:

```text
manifest.json + ui/ + mcp/
```

Authors call tools via `window.shell.callTool(...)` and never speak raw MCP from
the iframe. Manifest schema should support both local `stdio` and remote
`sse`/`http` MCP transports (implementation may ship stdio first).

When changing the manifest or bridge shape, update together: blueprint, PoC
example plugin, and (once they exist) `packages/contracts` + `packages/plugin-sdk`.

## Style guidelines

- Prefer small, focused changes that match existing docs and decisions.
- English for code, comments, docs, and user-facing UI copy.
- Do not invent root-level `server.js` / `public/` / `plugins/` for the product -
  that layout exists only under `docs/PoC/`. Scaffold toward
  `docs/backend-structure.md` §2 instead.
- Do not expand MVP scope with heavy frameworks “for cleanliness.”
- Keep domain pure; put I/O in infrastructure adapters.
- For UI: follow `.agents/skills/frontend-design/SKILL.md` - distinctive,
  subject-grounded design; avoid generic AI-default palettes and layouts.

### UI knowledge docs (required)

When changing launcher or plugin UI:

- Update the relevant sketch or PoC under `docs/ui-design/` or `docs/PoC/`
  if behavior/visual contracts changed.
- Keep product UX notes in `docs/blueprint.md` §4 when window modes or launcher
  interactions change.
- Do **not** invent a parallel `resources/webchat/docs/` tree - that path is not
  part of this project.

## Versioning

- Single source of truth for the release number: root `VERSION` (currently `0.0.1`).
- Follow [Semantic Versioning](https://semver.org/):
  - **MAJOR** - breaking changes to public contracts (manifest, WS protocol, plugin SDK)
  - **MINOR** - backward-compatible features
  - **PATCH** - backward-compatible fixes / docs-only releases when you choose to tag them
- When a change should ship, bump `VERSION` and add a Keep a Changelog section in
  `CHANGELOG.md` for that version (Added / Changed / Fixed / …).
- Concept-stage (`0.0.x`): prefer documenting notable scaffolding and doc/contract
  changes even when no binary ships yet.

## Testing

Until the monorepo is fully wired:

- PoC smoke: run `docs/PoC` and exercise Notes → Create Note; confirm bridge log
  and running badge.
- Domain unit tests: `cd packages/domain && npx vitest run` (or `pnpm test` from root
  once workspace install scripts are approved).
- There is no full workspace CI yet — do not invent CI commands that are not in the repo.

## Pull requests

Use `.github/pull_request_template.md`. Fill Description, Type of Change, and
test notes. Link `VERSION` / `CHANGELOG.md` when the PR is meant to ship.

## Out of scope for agents (unless explicitly asked)

- Choosing a public license
- Implementing the full security layer early
- Replacing the architecture with Socket.IO / Redis / microservices “defaults”
- Committing secrets or production credentials
