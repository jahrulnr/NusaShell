# NusaShell

**A shell platform for AI tools - install plugins like you install desktop apps.**

Every plugin bundles a UI *and* an MCP server together. NusaShell manages the process
lifecycle in the background and brokers communication between the two, so your
AI tools get a real visual home instead of living only as invisible backend
integrations.

---

## Overview

MCP (Model Context Protocol) solved the problem of giving AI models structured
access to tools and data. But MCP servers are backend-only by design - there's
no notion of a visual surface. If you want a dashboard, a form, a chat panel,
or any interactive UI in front of your tool, MCP alone doesn't cover it.

NusaShell fills that gap. It's a desktop-app-like shell: a launcher with a grid
of icons (think Android's app drawer, or a desktop taskbar), where each icon
represents an installed plugin. Tap an icon and its UI opens; behind the
scenes, NusaShell spawns (or reuses) that plugin's MCP server process on
demand and brokers every tool call between the UI and the server.

**Why this exists:**

- **AI tools deserve a real UI, not just a chat log.** A lot of tool interactions
(browsing data, filling a form, watching a live dashboard) are just better as
a visual surface than as text back-and-forth.
- **Install/uninstall should feel like a desktop app.** Drop a plugin in, it
shows up as an icon. Remove it, it's gone - no config file surgery.
- **UI and backend logic shouldn't need to trust each other directly.** The
shell sits in the middle as a broker, which keeps plugin lifecycle
(spawn, suspend, kill) and communication routing in one predictable place.
- **Plugin authors shouldn't have to reinvent MCP.** If you already have an
MCP server, you mostly just add a `ui/` folder next to it - NusaShell
handles the rest.



## How it works (short version)

```mermaid
sequenceDiagram
  participant User
  participant UI as Plugin UI
  participant Host as NusaShell host
  participant Backend as Backend broker
  participant MCP as Plugin MCP

  User->>Host: Click plugin icon
  Host->>UI: Open plugin UI window
  UI->>Host: window.shell.callTool
  Host->>Backend: WebSocket tool call
  Backend->>MCP: Forward call
  Note over Backend,MCP: Spawn on first use, reuse after
  MCP-->>Backend: Tool result
  Backend-->>Host: Relay result
  Host-->>UI: Bridge response
```

Plugin UI and MCP never peer-connect. The host talks to the backend over
WebSocket; the plugin iframe talks to the host via a small bridge API
(`window.shell.callTool`). See the docs map below for the full story.

## Screenshots

Captured from the Electron desktop app (`make dev`):

| Launcher | Agent workspace | Skills workspace |
| --- | --- | --- |
| ![NusaShell launcher](./docs/assets/screenshots/desktop-app.png) | ![Agent workspace with a live conversation](./docs/assets/screenshots/agent-workspace.png) | ![Skills workspace, empty-library state](./docs/assets/screenshots/skills-workspace.png) |
| Home grid with installed plugins (Files, Mail, Notes, Terminal) and live running-state badges. | Full-bleed conversation rail + message runway, streaming a real provider turn. | Three-pane package browser (library, files, editor) shown in its empty state. |

## Documentation map

| Doc | Role |
| --- | --- |
| [`AGENTS.md`](./AGENTS.md) | Agent/human working rules, architecture locks, versioning |
| [`docs/blueprint.md`](./docs/blueprint.md) | Product concept: plugin shape, launcher UX, lifecycle, MCP transports, runtime trade-offs |
| [`docs/backend-structure.md`](./docs/backend-structure.md) | Target backend: Clean Architecture monorepo, WebSocket protocol, package boundaries, MVP scope |
| [`docs/architecture/agent-runtime.md`](./docs/architecture/agent-runtime.md) | Agent turn loop, provider routing, tool-call recovery, conversation/checkpoint model |
| [`docs/architecture/agent-skills-platform-technical-spec.md`](./docs/architecture/agent-skills-platform-technical-spec.md) | Full technical spec for a general-purpose agent skills platform (draft, larger than the shipped subset) |
| [`docs/architecture/local-agent-skills.md`](./docs/architecture/local-agent-skills.md) | Current, shipped boundary of the local managed skills library and its read-only meta-tools |
| [`docs/architecture/agent-memory.md`](./docs/architecture/agent-memory.md) | Persistent agent memory (MEMORY.md + USER.md), snapshot injection, and the `memory` meta-tool |
| [`docs/architecture/mcp-capability-policy.md`](./docs/architecture/mcp-capability-policy.md) | Which MCP protocol capabilities NusaShell implements now vs. defers, and the adoption gate |
| [`docs/architecture/progressive-mcp-tools.md`](./docs/architecture/progressive-mcp-tools.md) | Shell-owned meta-tools used to keep MCP tool discovery bounded per agent turn |
| [`docs/architecture/job-automation.md`](./docs/architecture/job-automation.md) | Scheduled durable jobs (one-shot/recurring) that fire headless agent turns or plugin tool calls |
| [`docs/architecture/plugin-sandbox-readiness.md`](./docs/architecture/plugin-sandbox-readiness.md) | Files root-containment bundle guard, plugin process-death status SoT, and Tools=0 honesty mitigations |
| [`docs/architecture/workspace-mcp-binding.md`](./docs/architecture/workspace-mcp-binding.md) | How `conversation.workspace` binds to MCP (wrap → Roots → respawn/enable overrides) |
| [`docs/RISK.md`](./docs/RISK.md) | Residual risk register: agent MCP launch overrides (`npx` swap), advisory roots |
| [`docs/mcp/nusashell-mail-mcp-plugin-spec.md`](./docs/mcp/nusashell-mail-mcp-plugin-spec.md) | Mail plugin protocol assessment, security model, and target tool contract |
| [`docs/mcp/nusashell-mail-mcp-plugin-implementation.md`](./docs/mcp/nusashell-mail-mcp-plugin-implementation.md) | Implemented read-only Mail milestone, runtime wiring, and current limitations |
| [`docs/PoC/`](./docs/PoC/) | Runnable zero-dep bridge demo (behavioral reference, not the target layout) |
| [`docs/ui-design/`](./docs/ui-design/) | Launcher visual sketch, plus the Agent workspace and Skills workspace visual contracts |


## Prerequisites

- **Node.js 20+** and **pnpm 11+**
- **Native build tools** (required by `better-sqlite3`):
  - Linux: `sudo apt install python3 make g++` (or distro equivalent)
  - macOS: `xcode-select --install`
  - Windows: `npm install --global windows-build-tools` or Visual Studio Build Tools



## Quickstart (Desktop App)

Requires Node.js 20+, pnpm 11+, and native build tools (see [Prerequisites](#prerequisites)).

```bash
git clone https://github.com/jahrulnr/NusaShell.git nusashell
cd nusashell
pnpm install
make dev
```

This launches the Electron desktop app with the backend embedded.
The app starts in AI stub mode by default — configure a real provider
in Settings to enable actual AI responses.

Background learning (and future scheduled jobs) run only while NusaShell is
running. Enable **Keep running when window is closed** under Settings →
Startup & background so closing the window hides to the tray instead of
quitting. Optional **Launch at login** is available on packaged builds.

## Writing your own plugin

A plugin is just a folder with three things:

```
plugins/my-plugin/
├── manifest.json     # declares the UI entry point + how to start the MCP server
├── icon.png          # 512×512 launcher artwork
├── ui/
│   └── index.html    # rendered inside a window/iframe
└── mcp/
    └── server.js      # your MCP server - any language, runs as its own process
```

Minimal manifest:

```jsonc
{
  "id": "you.my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "icon": "file://icon.png",
  "ui": { "entry": "ui/index.html" },
  "mcp": { "transport": "stdio", "command": "node mcp/server.js" }
}
```

Inside your UI, call a tool without knowing anything about MCP directly:

```js
const result = await window.shell.callTool("myTool", { some: "args" });
```

NusaShell takes care of spawning your MCP process, routing the call, and
matching the response back to the right request.

The built-in plugins under `plugins/` include Notes and a read-only
Mail client. Mail demonstrates a larger plugin surface, multi-account
host-owned settings, and runtime-only credential delivery to an MCP process.

## Project status

This repo is **early scaffold with a working desktop app**: a pnpm monorepo with
implemented domain, application, infrastructure, transport, contracts, and plugin-sdk
packages; a backend composition root; and an Electron desktop shell. Architecture
docs and the PoC under `docs/` remain authoritative for product intent.

**Monorepo today:**

- `packages/domain` — plugin identity, manifest, runtime state, lifecycle policies, tool-call model (Vitest-covered)
- `packages/application` — command/query handlers, agent turn runner, prompt injection, docs tools
- `packages/infrastructure` — SQLite, filesystem registry, MCP clients (stdio/HTTP/SSE), Pino logger, docs index
- `packages/transport-ws` — WebSocket protocol, message router, session registry, event publisher
- `packages/contracts` — Zod request/response/event schemas and DTOs
- `packages/plugin-sdk` — `NusaClient` WebSocket client with reconnect
- `packages/{shared,testing}` — shared test fakes and helpers
- `apps/backend` — composition root wiring all layers
- `apps/desktop` — Electron shell with launcher, plugin windows, agent workspace, AI provider settings

**Persistent logs:** System logs (backend, agent, plugin, MCP) are written to
`userData/logs/nusashell.log` via Pino multistream (stdout + file append).
The file survives restarts and is the primary source for post-incident debugging.

**What the PoC demonstrates** (`docs/PoC/`):

- Plugin discovery (folder scan), manifest parsing
- Lazy MCP process spawning, one process per plugin, reused across calls
- UI ↔ shell ↔ MCP bridge over a simple request/response protocol
- Launcher UI with running-state indicators

**Target stack** (partially scaffolded): Electron + TypeScript monorepo (pnpm),
Clean Architecture packages, WebSocket client transport, official MCP TypeScript
SDK, SQLite for installed-plugin metadata. Domain layer is in place; application
and infrastructure are next.

**Deliberately deferred** (by design, to avoid premature complexity):

- Security: iframe sandboxing, install-time permission prompts, process
isolation - next phase after core plumbing, kept separate on purpose
- Swapping the PoC hand-rolled stdio JSON-RPC for `@modelcontextprotocol/sdk`
- Idle-timeout auto-suspend for MCP processes
- Installing from a packaged `.zip` instead of a raw folder
- True multi-window support (PoC uses a single modal window)



## Repo layout (today)

```
.
├── AGENTS.md
├── README.md
├── VERSION
├── CHANGELOG.md
├── package.json
├── pnpm-workspace.yaml
├── tsconfig.base.json
├── apps/
│   ├── backend/               # composition root (bootstrap, container, shutdown)
│   └── desktop/               # Electron shell (launcher, plugin windows, agent workspace)
├── packages/
│   ├── domain/                # implemented (plugin runtime, policies, events)
│   ├── application/           # implemented (handlers, agent runner, prompts, docs tools)
│   ├── infrastructure/        # implemented (SQLite, FS registry, MCP clients, logger)
│   ├── transport-ws/          # implemented (WS protocol, router, sessions, events)
│   ├── contracts/             # implemented (Zod schemas, DTOs)
│   ├── plugin-sdk/            # implemented (NusaClient, reconnect)
│   ├── shared/                # stub
│   └── testing/               # test fakes and helpers
└── docs/
    ├── blueprint.md           # product / plugin architecture
    ├── backend-structure.md   # target backend monorepo + WS protocol
    ├── architecture/          # agent runtime, skills platform, MCP capability/tooling policy
    ├── mcp/                   # Mail plugin protocol spec + implementation notes
    ├── assets/screenshots/    # README screenshots of the running desktop app
    ├── PoC/                   # runnable zero-dep bridge demo
    └── ui-design/             # launcher, agent workspace, and skills workspace visual sketches
```

Further monorepo pieces (`plugins/`, full apps) are specified in
[`docs/backend-structure.md`](./docs/backend-structure.md) §2 and land in later milestones.