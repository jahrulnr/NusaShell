# NusaShell - PoC

Proof of concept: a shell (host app) with an Android-style app-drawer launcher.
Each “app” is a plugin that bundles **UI** + an **MCP server**. Click an icon →
UI opens → UI calls a tool → shell spawns/reuses the MCP process in the
background → result returns to the UI.

Plain Node.js with **no npm dependencies** (no `npm install` required) so it
runs anywhere Node.js is installed.

This PoC is a **behavioral reference**. The target product layout is the Electron
+ Clean Architecture monorepo in [`../backend-structure.md`](../backend-structure.md);
product/plugin concepts live in [`../blueprint.md`](../blueprint.md).

## How to run

```bash
node server.js
```

Then open **http://localhost:8420** in a browser.

- Click the **Notes** icon → the plugin window opens (iframe)
- Type a note, click **Create Note** → this sends `window.parent.postMessage(...)`
  which the launcher bridges to the shell backend → the shell spawns
  `plugins/notes/mcp/server.js` (if not already running) → that process handles
  the tool call → the result returns to the UI
- Watch the **green badge** on the Notes icon after the first tool call - that
  means plugin state moved `idle` → `running` (MCP process still alive)
- The log panel at the bottom of the launcher shows live bridge traffic
  (`UI -> shell : tool_call` and `shell -> UI : tool_result`)
- Closing the plugin window (✕) leaves the MCP process **alive**. The manifest
  sets `keepAliveOnClose: false`, but this PoC does not yet auto-kill on idle,
  so the process stays until the server restarts - that idle/suspend path is a
  next step in the blueprint

## Layout

```
docs/PoC/
├── server.js              # Shell backend: HTTP server, plugin registry, MCP process manager, bridge broker
├── public/
│   ├── launcher.html       # Launcher UI (icon grid)
│   └── launcher.js         # Launcher logic + bridge (postMessage from iframe → shell backend)
└── plugins/
    └── notes/
        ├── manifest.json    # UI entry + MCP command
        ├── ui/index.html    # Plugin UI (iframe); uses window.parent.postMessage for callTool()
        └── mcp/server.js    # Dummy MCP server (separate child process), JSON-RPC over stdio
```

## Why not the official `@modelcontextprotocol/sdk`?

In the original PoC environment, npm registry access was blocked
(`403 host_not_allowed`), so JSON-RPC is implemented by hand in `mcp/server.js`.
Methods intentionally mirror the real MCP shape (`initialize`, `tools/list`,
`tools/call`) so swapping to the official SDK later does not require changing
shell architecture. On a machine with normal registry access:

```bash
npm install @modelcontextprotocol/sdk
```

Then rewrite `mcp/server.js` with `Server` + `StdioServerTransport` from the SDK,
and rewrite the spawn+JSON path in `server.js` with `Client` +
`StdioClientTransport`. Bridge and lifecycle logic stay the same.

## Next steps (from the blueprint)

- [ ] Swap hand-rolled JSON-RPC → official MCP SDK
- [ ] Idle timeout → auto-suspend unused MCP processes
- [ ] Install from `.zip` (not only folder-scan under `plugins/`)
- [ ] Multiple plugin windows open at once (not only one modal)
- [ ] Security layer: iframe sandbox attribute, install-time permission dialog
