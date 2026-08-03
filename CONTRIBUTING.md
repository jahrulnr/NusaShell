# Contributing to NusaShell

Thanks for helping build NusaShell, a shell platform for AI tools. Contributions
happen through this public GitHub repository — use issues and pull requests here.

## Set up a checkout

Prerequisites:

- Node.js 20 or newer
- pnpm 11 or newer
- Native build tools for `better-sqlite3`
  - Linux: Python 3, `make`, and a C++ compiler (for example,
    `sudo apt install python3 make g++`)
  - macOS: Xcode Command Line Tools (`xcode-select --install`)
  - Windows: Visual Studio Build Tools with the native C++ workload

Clone and install:

```bash
git clone https://github.com/jahrulnr/NusaShell.git nusashell
cd nusashell
pnpm install
```

Start the desktop development app with:

```bash
make dev
```

`pnpm desktop:dev` is the direct package-script alternative. Unpackaged `--dev`
runs keep durable state in `<repo>/.nusashell/`.

## Repository layout

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
│   ├── domain/                # plugin identity, manifest, runtime state, lifecycle policies (Vitest-covered)
│   ├── application/           # command/query handlers, agent turn runner, prompt injection, docs tools
│   ├── infrastructure/        # SQLite, filesystem registry, MCP clients (stdio/HTTP/SSE), Pino logger
│   ├── transport-ws/          # WebSocket protocol, message router, session registry, event publisher
│   ├── contracts/             # Zod request/response/event schemas and DTOs
│   ├── plugin-sdk/            # `NusaClient` WebSocket client with reconnect
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
[`docs/backend-structure.md`](./docs/backend-structure.md) §2 and land in later
milestones.

## Before changing code

Read [`AGENTS.md`](./AGENTS.md) first — it holds the architecture locks that PRs
must not violate:

- **Broker only:** plugin UI and MCP never peer-connect; all traffic goes
  through the shell.
- **Two transport layers:** plugin iframe ↔ host uses `postMessage` /
  `window.shell.callTool`; host ↔ backend uses WebSocket commands/queries —
  not an internal event bus.
- **Dependency rule:** `packages/domain` must not import Electron, WebSocket,
  SQLite, `child_process`, filesystem, MCP SDK, HTTP, or SSE.
- **Runtime SoT:** live plugin runtime belongs to `PluginRuntimeManager`; do not
  duplicate authoritative "running" state elsewhere.

Relevant background docs: [`docs/blueprint.md`](./docs/blueprint.md) for
product/plugin UX, [`docs/backend-structure.md`](./docs/backend-structure.md)
for backend layers and the WebSocket protocol, and [`docs/architecture/`](./docs/architecture/)
for the agent runtime and MCP tool policy.

For plugin work, read
[`resources/agent/docs/build-plugin.md`](./resources/agent/docs/build-plugin.md)
and use `.agents/skills/build-nusashell-plugin/`. Headless MCP-only plugins and
windowed plugins have different user experiences — do not add a UI just to make
a headless integration appear on Home.

## Verify your change

Run the focused tests for the package you changed, then the repository checks:

```bash
pnpm test:frontend
pnpm test:backend
pnpm typecheck
```

If you add, rename, remove, or change a `data-view`, view control, button,
modal, or interaction in `apps/desktop/src/renderer/`, update
`resources/agent/docs/ui-source/ui-map.json` and regenerate the UI docs:

```bash
pnpm scan:ui-docs
```

Never edit `resources/agent/docs/ui/*.md` by hand — they are generated.

## Submit a pull request

- Use the template at
  [`.github/pull_request_template.md`](./.github/pull_request_template.md) —
  fill Description, Type of Change, and test notes.
- Keep changes small and focused; English for code, comments, docs, and
  user-facing UI copy.
- Keep `VERSION` unchanged while work is in progress. Only bump
  [`VERSION`](./VERSION) at the release boundary and add a matching Keep a
  Changelog section in [`CHANGELOG.md`](./CHANGELOG.md) in the same change —
  see [versioning rules](./AGENTS.md#versioning).
- Do not commit secrets, production credentials, or generated build output.

## Continuous integration and releases

GitHub Actions runs the desktop/frontend tests and backend/package/plugin tests
in parallel on Linux, Windows, and macOS with Node.js 24. Once every test matrix
entry passes, Electron Forge builds native distributables on the same three
platforms and stores them as workflow artifacts for 14 days.

Pull requests and manual workflow runs stop after that build. A GitHub Release
is created only on pushes to `master` (including merged PRs): it tags
`v$(cat VERSION)`, attaches the install payloads (`latest.json`, tar.gz,
checksums, and Forge artifacts), and uses the matching `CHANGELOG.md` section
as release notes. If that tag already exists, publish is skipped until
`VERSION` is bumped.

## Out of scope

Unless a maintainer explicitly asks, do not open PRs that:

- Choose or change a public license.
- Implement host isolation (iframe sandbox, install permissions, process
  isolation) ahead of the core plumbing milestones.
- Add MCP/AI behavioral hardening (tool-call approval gates, injection
  filters, plugin allowlists) — permanently out of scope per
  [`docs/architecture/security-boundary.md`](./docs/architecture/security-boundary.md).
- Replace the architecture with Socket.IO / Redis / microservices defaults.

## Reporting security issues

See [`SECURITY.md`](./SECURITY.md).
