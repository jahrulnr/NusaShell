# AGENTS.md - NusaShell

Instructions for humans and coding agents working in this repository.

## Project snapshot

NusaShell is a desktop-like **shell for AI tools**: each plugin bundles a **UI +
MCP server**; the shell brokers lifecycle and tool calls so plugins get a real
visual surface (not chat-only MCP).

**Current stage:** working desktop app on top of a pnpm monorepo. `domain`,
`application`, `infrastructure`, `transport-ws`, `contracts`, and `plugin-sdk`
packages are implemented; `apps/backend` (composition root) and `apps/desktop`
(Electron shell) are implemented. `packages/shared` and `packages/testing` are
still stub/helper-only. Architecture docs and `docs/PoC/` remain the
authoritative behavioral reference for product intent.

| Path | Role |
| --- | --- |
| `README.md` | Product intent, docs map, PoC quickstart |
| `packages/domain/` | Pure domain layer (plugin runtime, policies, events) |
| `docs/blueprint.md` | Product / plugin architecture, launcher UX, MCP transports |
| `docs/backend-structure.md` | Target Clean Architecture monorepo + WebSocket protocol |
| `docs/architecture/` | Agent runtime, agent skills platform, MCP capability policy, progressive MCP tools, path layout |
| `docs/mcp/` | Mail plugin MCP protocol spec + implementation notes |
| `resources/agent/docs/` | In-app product docs and FAQ corpus; use `docs_search` for product questions |
| `docs/PoC/` | Behavioral bridge demo (not the target layout) |
| `docs/ui-design/` | Launcher, agent workspace, and skills workspace visual sketches |
| `VERSION` | Current semver |
| `CHANGELOG.md` | User-facing notable changes |
| `.agents/skills/frontend-design/` | Distinctive UI design skill for shell/plugin surfaces |

## Start here (every task)

1. Read this file and the relevant section of `README.md`.
2. For product/plugin UX → `docs/blueprint.md`.
3. For backend folders, layers, WS protocol, MVP scope → `docs/backend-structure.md`.
4. For agent runtime, MCP tool policy, or local skills → `docs/architecture/`.
5. Treat `docs/PoC/` as a **behavioral reference**, not the scaffold target.
6. For launcher / plugin UI work → also load `.agents/skills/frontend-design/SKILL.md`.
7. For product questions about data locations, uninstalling, contributing, or
   authoring plugins, use the matching documents in `resources/agent/docs/`
   (`data-locations.md`, `uninstall.md`, `contribute.md`, and
   `build-plugin.md`) and condition answers on Linux, macOS, or Windows. Never
   present `~/.config/nusashell/` as a universal path; use `mcp_list.installPath`
   for a specific installed plugin.

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
  - Host ↔ backend: **Electron IPC** (`ipcMain.handle("shell:request")` → `MessageRouter` → command/query bus) — **not** an internal event bus. WebSocket is a legacy/optional adapter for non-Electron hosts; the desktop product path does not start the WS server (`startWsServer: false`).
- **Dependency rule:** `domain` must not import Electron, WebSocket, SQLite, `child_process`, filesystem, MCP SDK, HTTP, or SSE.
- **Runtime SoT:** live plugin runtime belongs to `PluginRuntimeManager` (memory). Installed metadata → SQLite (filesystem/JSON OK only as an early spike). Do not duplicate authoritative “running” state in the renderer, WS gateway, or DB.
- **Infrastructure must not** send WebSocket frames directly - publish domain/application events.
- **Host isolation is deferred** until broker/lifecycle correctness is proven. Do not mix iframe sandboxing, install permissions, signing, or process isolation into the first plumbing milestone. Do **not** treat MCP/AI behavioral hardening (tool-call approval gates, injection filters, plugin allowlists) as deferred NusaShell work — that stays permanently out of scope; see `docs/architecture/security-boundary.md`.
- **MVP stays slim:** no Redis, microservices, event sourcing, external CQRS frameworks, Socket.IO-in-core, or clustered workers for the first MVP.

Target stack (when scaffolding): Electron + TypeScript monorepo (pnpm), packages
`domain` / `application` / `infrastructure` / `transport-ws` / `contracts` /
`plugin-sdk`, Zod, official MCP TypeScript SDK, SQLite (`better-sqlite3`),
Vitest, Pino. Details: `docs/backend-structure.md` §2 / §18 / §19.

## Plugin contract

A plugin is one folder:

```text
manifest.json + mcp/            # headless MCP-only plugin
manifest.json + ui/ + mcp/      # plugin with a window (UI + MCP)
```

`ui/` is **optional**. Omit it for a headless MCP-only plugin (no window, not on
the Home launcher grid; managed from the Plugins view and via agent `mcp_*`
tools). `icon` stays required (emoji/text like `N` / `📝` is valid). When
`ui.entry` is declared, the installer `access()`es the resolved file under the
plugin folder and fails the install early if it is missing or escapes the plugin
dir; local file icons get the same check.

Authors call tools via `window.shell.callTool(...)` and never speak raw MCP from
the iframe. Manifest schema should support both local `stdio` and remote
`sse`/`http` MCP transports (implementation may ship stdio first). Plugin-specific
capability knowledge belongs to live tool discovery and plugin-owned MCP prompts,
not the shell's `resources/agent/docs/` corpus. For authoring, follow the domain
versus native-like prompt tiers in `.agents/skills/build-nusashell-plugin/`.
In-app authoring targets `{userData}/plugins/` and must finish with interactive
`mcp_register`; repository `plugins/` is for Cursor/monorepo development only.

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

## Cross-platform support

Linux, Windows, and macOS are first-class supported targets. GitHub Actions
tests the frontend and backend independently on all three platforms, then
builds the desktop distributable on the same platform matrix defined in
`.github/workflows/ci.yml`. Every change must respect that contract.

- Prefer Node.js and package APIs over shell-specific commands, syntax, or
  environment variables. Keep reusable logic out of `bash`, PowerShell, and
  POSIX-only utilities unless the workflow explicitly scopes it to one OS.
- Use `node:path`, `node:os`, and `node:fs` APIs for paths, temporary files,
  home directories, and filesystem operations. Never hardcode `/`, `\\`,
  `/tmp`, `/home`, `C:\\`, or assume a case-sensitive filesystem.
- Do not assume `process.cwd()` is a stable application or user workspace;
  resolve paths from explicit configuration and platform-aware helpers.
- Keep platform-specific behavior isolated behind a small adapter or guarded
  branch, and add/adjust tests when the behavior differs by OS.
- Validate changes with the root package scripts used by CI, especially
  `pnpm test:frontend`, `pnpm test:backend`, and the desktop build path. Do not
  weaken or skip a platform matrix job to make a change pass.

### UI knowledge docs (required)

When changing launcher or plugin UI:

- Update the relevant sketch or PoC under `docs/ui-design/` or `docs/PoC/`
  if behavior/visual contracts changed.
- Keep product UX notes in `docs/blueprint.md` §4 when window modes or launcher
  interactions change.
- Update `resources/agent/docs/ui-source/ui-map.json` and regenerate
  `resources/agent/docs/ui/*.md` by running `pnpm scan:ui-docs` whenever a
  `data-view`, view control, button, modal, or interaction in
  `apps/desktop/src/renderer/` is added, renamed, removed, or changed.
  The `prebuild` hook runs the scanner and fails if any view is undocumented
  or a mapped control ID is missing from source.
- Do **not** edit `resources/agent/docs/ui/*.md` files manually; they are
  generated from the UI map.
- Do **not** invent a parallel `resources/webchat/docs/` tree - that path is not
  part of this project.

## Versioning

- Single source of truth for the release number: root `VERSION`; do not copy the
  current version number into this file.
- Follow [Semantic Versioning](https://semver.org/):
  - **MAJOR** - breaking changes to public contracts (manifest, WS protocol, plugin SDK)
  - **MINOR** - backward-compatible features
  - **PATCH** - backward-compatible fixes / docs-only releases when you choose to tag them
- Keep `VERSION` unchanged while work is local and uncommitted. Do not bump the
  version merely because files changed or a plan is complete.
- Only bump `VERSION` at the release boundary, when the final change is ready to
  be committed and pushed. Add the matching Keep a Changelog section in the
  same release change. If a release is not being committed and pushed yet, keep
  the existing version and leave release notes for later.
- Concept-stage (`0.0.x`): prefer documenting notable scaffolding and doc/contract
  changes even when no binary ships yet, but apply the same release-boundary rule.

## Testing

### Red–green–refactor

When adding or changing behavior that already has (or should have) automated
tests, work in this order:

1. **Red** — write or extend a failing test that names the desired behavior
   (or reproduces the bug). Run it and confirm it fails for the right reason.
2. **Green** — implement the smallest change that makes the test pass. Do not
   broaden scope while the suite is red.
3. **Refactor** — clean structure, names, and duplication only while tests stay
   green. No behavior change without returning to red/green.

Rules of thumb:

- Prefer one behavior slice per red–green cycle; avoid multi-feature batches
  that skip a failing test.
- Bug fixes: failing reproduction first, then the fix.
- Pure docs, generated UI map output, or trivial renames may skip the cycle
  when no testable contract changes.
- Do not delete or weaken a test only to go green; fix the product or adjust
  the assertion to the agreed contract.
- Keep domain pure and I/O in infrastructure; tests should pin the contract
  the cycle is proving.

Until the monorepo is fully wired:

- PoC smoke: run `docs/PoC` and exercise Notes → Create Note; confirm bridge log
  and running badge.
- Domain unit tests: `cd packages/domain && npx vitest run` (or `pnpm test` from root
  once workspace install scripts are approved).
- GitHub Actions runs `pnpm test:frontend` and `pnpm test:backend` independently
  on Linux, Windows, and macOS before allowing the three-platform build matrix.
  Keep CI commands backed by root `package.json` scripts.

## Pull requests

Use `.github/pull_request_template.md`. Fill Description, Type of Change, and
test notes. Link `VERSION` / `CHANGELOG.md` when the PR is meant to ship.

## Out of scope for agents (unless explicitly asked)

- Choosing a public license
- Implementing host isolation (iframe sandbox, install permissions, process isolation) early
- Building MCP/AI behavioral security (approval gates, injection filters, plugin allowlists) — permanently out of scope per `docs/architecture/security-boundary.md`
- Replacing the architecture with Socket.IO / Redis / microservices “defaults”
- Committing secrets or production credentials
