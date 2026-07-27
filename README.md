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

```
click icon → NusaShell opens plugin's UI in a window
           → UI calls a tool via the shell bridge
           → NusaShell (as broker) forwards the call to the plugin's MCP process
              (spawning it on first use, reusing it after that)
           → MCP server executes the tool, returns a result
           → NusaShell relays the result back to the UI
```

Plugin UI and MCP never peer-connect. In the target architecture the host talks
to the backend over WebSocket; the plugin iframe talks to the host via a small
bridge API (`window.shell.callTool`). See the docs map below for the full story.

## Documentation map

| Doc | Role |
| --- | --- |
| [`AGENTS.md`](./AGENTS.md) | Agent/human working rules, architecture locks, versioning |
| [`docs/blueprint.md`](./docs/blueprint.md) | Product concept: plugin shape, launcher UX, lifecycle, MCP transports, runtime trade-offs |
| [`docs/backend-structure.md`](./docs/backend-structure.md) | Target backend: Clean Architecture monorepo, WebSocket protocol, package boundaries, MVP scope |
| [`docs/PoC/`](./docs/PoC/) | Runnable zero-dep bridge demo (behavioral reference, not the target layout) |
| [`docs/ui-design/`](./docs/ui-design/) | Launcher visual sketch |

## Quickstart (PoC)

Requires Node.js only (no `npm install` for the proof of concept).

```bash
git clone <your-repo-url> nusashell
cd nusashell/docs/PoC
node server.js
```

Then open **http://localhost:8420** in your browser.

- Click the **Notes** icon → its UI opens in a window
- Type a note, click **Create Note** → this calls a tool through the bridge,
  which spawns the plugin's MCP process on first use
- Watch the live log panel at the bottom of the launcher - it shows the actual
  `tool_call` / `tool_result` traffic going through the broker
- The icon gets a green "running" badge once its MCP process is alive

More detail: [`docs/PoC/README.md`](./docs/PoC/README.md).

## Writing your own plugin

A plugin is just a folder with three things:

```
plugins/my-plugin/
├── manifest.json     # declares the UI entry point + how to start the MCP server
├── ui/
│   └── index.html    # rendered inside a window/iframe
└── mcp/
    └── server.js      # your MCP server - any language, runs as its own process
```

Minimal manifest:

```jsonc
{
  "id": "com.you.my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "icon": "🔧",
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

## Project status

This repo is **concept-stage**: architecture and a small PoC live under `docs/`.
There is no `apps/` / `packages/` monorepo yet - that is the next build target
described in [`docs/backend-structure.md`](./docs/backend-structure.md).

**What the PoC demonstrates today** (`docs/PoC/`):
- Plugin discovery (folder scan), manifest parsing
- Lazy MCP process spawning, one process per plugin, reused across calls
- UI ↔ shell ↔ MCP bridge over a simple request/response protocol
- Launcher UI with running-state indicators

**Target stack** (not scaffolded yet): Electron + TypeScript monorepo (pnpm),
Clean Architecture packages, WebSocket client transport, official MCP TypeScript
SDK, SQLite for installed-plugin metadata.

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
└── docs/
    ├── blueprint.md           # product / plugin architecture
    ├── backend-structure.md   # target backend monorepo + WS protocol
    ├── PoC/                   # runnable zero-dep bridge demo
    └── ui-design/             # launcher visual sketch
```

Target monorepo layout (`apps/`, `packages/`, `plugins/examples/`, …) is specified
in [`docs/backend-structure.md`](./docs/backend-structure.md) §2 - it is not created
in the tree yet.

## License

Not yet decided - add one before sharing this publicly (MIT is a reasonable
default for a project at this stage if you don't have other constraints).
