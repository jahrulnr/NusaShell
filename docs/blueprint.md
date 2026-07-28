# NusaShell Blueprint

> Product concept: a desktop-app-like **shell**. Each plugin is a **UI + MCP server**
> bundle. Install a plugin → its icon appears in the launcher (Android app-drawer
> style) → tap → open the UI; the MCP process is spawned in the background as needed.
>
> **Role of this doc:** product / plugin architecture and UX. Backend monorepo,
> Clean Architecture, and the WebSocket protocol live in
> [`backend-structure.md`](./backend-structure.md). The runnable PoC lives in
> [`PoC/`](./PoC/).
>
> **Scope note:** security (sandboxing, permission enforcement, signing) is
> intentionally **not** detailed in this phase. Focus first on architectural
> correctness and DX. Security is added later as a separate layer, not mixed in
> from day one.

---

## 1. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     SHELL HOST (Electron)                    │
│                                                               │
│  ┌───────────────┐   ┌──────────────────┐   ┌─────────────┐ │
│  │   Launcher    │   │   Window Manager  │   │  Settings   │ │
│  │  (grid icon)  │   │  (multi-window/   │   │   Panel     │ │
│  │               │   │   tab per plugin) │   │             │ │
│  └───────────────┘   └──────────────────┘   └─────────────┘ │
│           │                    │                              │
│  ┌────────▼────────────────────▼─────────────────────────┐  │
│  │         Host client (NusaClient over WebSocket)         │  │
│  └───────────────────────────┬───────────────────────────┘  │
└──────────────────────────────┼──────────────────────────────┘
                               │ WebSocket (request/response + events)
┌──────────────────────────────▼──────────────────────────────┐
│              BACKEND (Clean Architecture core)               │
│  Plugin Manager: install/registry/lifecycle/tool broker      │
│  ┌─────────────────────┐          ┌──────────────────────┐  │
│  │ MCP Client adapter  │          │ Process / FS / DB    │  │
│  │ (stdio | sse | http)│          │                      │  │
│  └──────────┬──────────┘          └──────────────────────┘  │
└─────────────┼───────────────────────────────────────────────┘
              │
   ┌──────────▼──────────┐
   │  Plugin MCP Server  │   (child process or remote endpoint)
   │  stdio / sse / http │
   └─────────────────────┘

Plugin UI (iframe) ──postMessage / window.shell──▶ Host renderer
     ▲                                                  │
     └──────────── tool_result via bridge ◀─────────────┘
```

The two plugin sides (**UI** and **MCP**) **never peer-connect**. Everything goes
through the Shell as broker. That keeps lifecycle management in one place (one
kill path, not many connections to chase).

### 1.1 Two transport layers (do not conflate)

| Layer | Mechanism | Role |
| --- | --- | --- |
| Plugin UI ↔ Host renderer | `postMessage` + `window.shell.callTool` helper | Plugin-author DX; the iframe never speaks raw MCP |
| Host ↔ Backend | WebSocket (`ws`), protocol in `backend-structure.md` | Commands/queries + state events; **not** an internal event bus |

The PoC under `docs/PoC/` collapses both layers into one Node + HTTP process for
easy demos. That is a **behavioral reference**, not the target layout.

---

## 2. Language & Runtime Choices for the Shell Backend

Compared across four axes: **language/ecosystem**, **cross-platform
compatibility**, **development ease**, and **user install ease**.

### Option A - Go

| Aspect | Notes |
| --- | --- |
| Compatibility | Compiles to a single binary per OS (Windows/Linux/macOS); no separate runtime |
| Dev ease | Goroutines are a good fit for many MCP processes. Embedded UI/webview ecosystem is thinner |
| User install | **Very easy** - one binary, double-click / run, no dependency |
| MCP client lib | Community SDK (`mcp-go`); stdio/sse well supported |
| UI hosting | Needs a webview bind (`webview/webview_go` → WebKit/WebView2) or a fuller alternative like **Wails** |
| Verdict | Strong when priorities are fast startup, light resources, simple distribution. **Wails** (Go backend + web frontend, native webview) is a solid candidate |

### Option B - Rust

| Aspect | Notes |
| --- | --- |
| Compatibility | Same as Go: single binary, solid cross-compile |
| Dev ease | Steeper learning curve, but **Tauri** is mature for this case - Rust backend + web frontend + native webview, plus its own plugin system (useful as a design reference, not only as a host) |
| User install | Native installers per OS (msi/dmg/AppImage); small size (does not ship Chromium like Electron) |
| MCP client lib | Official Anthropic Rust SDK (`rmcp`); process spawn is straightforward via `tokio::process` |
| Verdict | If the team already knows Rust or is willing to learn, **Tauri** is the most “ready-made” fit for a shell + plugin pattern - Tauri’s own plugin ideas map closely to what we want |

### Option C - Electron (Node.js)

| Aspect | Notes |
| --- | --- |
| Compatibility | Most proven cross-platform story, but Chromium+Node bundle is large (100MB+) |
| Dev ease | **Fastest for prototyping** - one language (JS/TS) for shell, UI, and even plugin MCP servers. npm covers child_process, IPC, etc. out of the box |
| User install | Large installer, but familiar UX (VS Code, Slack, Discord are all Electron) |
| MCP client lib | Official TypeScript SDK is the most complete and up to date |
| Verdict | Best when **development speed** matters and the team (including future plugin authors) is JS/TS-native. Trade-off is size and memory |

### Option D - .NET (MAUI/Avalonia + WebView)

| Aspect | Notes |
| --- | --- |
| Compatibility | Most native on Windows; Linux/macOS take more effort (Avalonia helps cross-platform) |
| Dev ease | C# is comfortable for structured plugin systems (interfaces, built-in DI) |
| User install | Needs a .NET runtime (or a self-contained publish that grows the binary) |
| Verdict | Candidate mainly for Windows-enterprise audiences. Weaker fit for a general/cross-platform target |

### Recommendation

Given priorities of **compat + dev ease + install ease**, and a build-first /
fast-exploration style:

- **Fastest velocity now (prototyping / near-term product):** **Electron + TypeScript**.
  One language across layers; VS Code’s extension model is a close reference
  (extension contributes UI + backend logic).
- **Mid-term “production-grade” path:** **Tauri (Rust backend)**. Smaller size,
  native webview per OS, and Tauri’s plugin API as a direct design reference
  (or even a host to build on, instead of reinventing the shell).

Plugin MCP servers **do not** need to share the shell’s language - they talk over
a separate process (stdio/sse/http). Authors can use Python/Go/Node/anything.
The shell only needs to spawn/connect and speak standard MCP.

---

## 3. Plugin Architecture

### 3.1 Package layout

```
my-plugin/
├── manifest.json          # metadata + entry points
├── icon.png               # 512x512, for the launcher
├── ui/
│   ├── index.html
│   ├── bundle.js          # UI logic; talks via the bridge
│   └── style.css
├── mcp/
│   ├── server.js          # or any-language binary/script
│   └── package.json       # (if the MCP server is Node-based)
└── README.md
```

### 3.2 Manifest spec (early draft)

```jsonc
{
  "id": "com.example.notes-plugin",
  "name": "Notes",
  "version": "1.0.0",
  "icon": "icon.png",

  "ui": {
    "entry": "ui/index.html",
    "window": {
      "mode": "panel",       // "panel" | "fullscreen" | "widget"
      "defaultSize": { "width": 800, "height": 600 },
      "resizable": true
    }
  },

  "mcp": {
    "transport": "stdio",     // "stdio" | "sse" | "http"
    "command": "node mcp/server.js",
    "env": {},                // optional env vars at spawn
    "autostart": false,       // spawn at install, or lazy on open?
    "keepAliveOnClose": false // keep MCP alive after the UI closes?
  },

  "dependencies": {
    "shell": ">=1.0.0"        // minimum shell version compatibility
  }
}
```

Design notes:

- `mcp.command` is a general command string - the shell just `spawn()`s a process
  and does not care which language is behind it (Node/Python/Go binary are all
  valid if executable on the target environment).
- `autostart` vs lazy spawn: default is **lazy** (spawn when the user first opens
  the UI) so idle cost stays low - similar to an Android Service bound on demand.
- `keepAliveOnClose`: for plugins with background work (file watchers, scheduled
  sync). The MCP process stays up after the window closes and shows a “running”
  badge on the launcher icon.

### 3.3 Communication Bridge

Iframe layer (author DX):

```
Plugin UI (iframe)
      │  postMessage({ type: "tool_call", tool: "createNote", args: {...}, requestId })
      ▼
Host renderer bridge
      │  NusaClient / WebSocket → backend CallTool use case
      ▼
MCP Client adapter → MCP Server (child process or remote)
      │
      ▼  MCP response
Host renderer bridge
      │  postMessage({ type: "tool_result", requestId, result })
      ▼
Plugin UI (resolve promise by requestId)
```

Pair requests and responses with a `requestId` (UUID) so the UI can have multiple
pending calls without races. The shell exposes a small helper into each iframe
(via `postMessage` + preload), roughly:

```js
// Injected into each plugin UI context
window.shell = {
  callTool: (toolName, args) => {
    const requestId = crypto.randomUUID();
    return new Promise((resolve) => {
      pendingCalls.set(requestId, resolve);
      parent.postMessage({ type: "tool_call", tool: toolName, args, requestId }, "*");
    });
  }
};
```

Plugin authors call `await window.shell.callTool("createNote", { text })` and never
need raw MCP details - the shell owns that. Command bus / WebSocket frames:
[`backend-structure.md`](./backend-structure.md).

### 3.4 Plugin Registry (local state)

**Installed metadata** (id, version, path, settings) is persisted in local app data.
**Live runtime state** (starting/running/etc.) is **not** owned by the registry -
it belongs to the backend runtime manager (see `backend-structure.md` §17).

PoC / early spikes may use flat JSON + folder scan. The target monorepo uses
SQLite for installed metadata; a filesystem registry is still fine in the earliest
phase before SQLite is wired.

```jsonc
// example "installed" projection (not the SoT for live runtime)
{
  "installed": [
    {
      "id": "com.example.notes-plugin",
      "version": "1.0.0",
      "installedAt": "2026-07-27T10:00:00Z",
      "path": "/plugins/com.example.notes-plugin"
    }
  ]
}
```

### 3.5 Lifecycle State Machine

Normative states follow the backend (`backend-structure.md`):

```text
idle | starting | running | background | stopping | crashed | disabled
```

Conceptual flow (UI / user-facing):

```
   [install]
       │
       ▼
   ┌───────┐  open / start   ┌──────────┐     ┌─────────┐
   │ Idle  │────────────────▶│ Starting │────▶│ Running │
   └───────┘                 └──────────┘     └────┬────┘
       │                                           │
       │                    close (keepAlive=false)│ stop
       │                           ┌───────────────┤
       │                           ▼               ▼
       │                     ┌──────────┐    ┌─────────┐
       │                     │ Stopping │───▶│  Idle   │
       │                     └──────────┘    └─────────┘
       │                           │
       │     close (keepAlive=true)│
       │                           ▼
       │                     ┌────────────┐  idle timeout  ┌───────────┐
       │                     │ Background │───────────────▶│ (suspend  │
       │                     └────────────┘                │  → idle / │
       │                                                   │  respawn) │
       │                                                   └───────────┘
       │     unexpected process exit ──▶ crashed ──▶ (recovery / starting)
       │
       ▼ [uninstall]
   ┌───────────┐
   │  Removed  │  (kill proc if still alive, delete folder, drop registry entry)
   └───────────┘
```

The frontend may cache a state projection for launcher badges; **authoritative**
live state stays in `PluginRuntimeManager` (backend).

### 3.6 Install / Uninstall Flow (simplified; no security checks yet)

**Install:**

1. User drops a `.zip` / `.tar.gz` package (or later a marketplace URL)
2. Shell extracts into `plugins/<id>/`
3. Parse `manifest.json`, validate required fields
4. If the MCP side needs dependency install (e.g. `npm install` in the MCP folder), run it once at install
5. Register in the installed-metadata store
6. Icon appears in the launcher

**Uninstall:**

1. Kill the MCP process if still running
2. Delete `plugins/<id>/`
3. Remove the registry entry
4. Icon disappears from the launcher

---

## 4. UI Design - Launcher (Android-style)

### 4.1 Layout

```
┌──────────────────────────────────────────────┐
│  🔍 Search plugins...              ⚙️ Settings │
├──────────────────────────────────────────────┤
│                                                │
│   [📝]      [🌤️]      [📅]      [➕]          │
│  Notes    Weather   Calendar  Install New     │
│                                                │
│   [💬]      [📊]      [🎨]                     │
│   Chat     Analytics  Design                  │
│                                                │
├──────────────────────────────────────────────┤
│  ● Notes                        ● Weather    │
└──────────────────────────────────────────────┘
```

- Icon grid; click → open the plugin window/panel
- Small badge on the icon when the plugin is `running` / `background`
- Long-press / right-click → context menu: **Open, Force Stop, Uninstall, Settings**
- Search bar to filter when many plugins are installed
- `➕ Install New` → file picker (later: marketplace browser)
- Launcher navigation is **Home**, **Plugins**, **Agent**, **AI Providers**, **Autostart**, and **Logs**. The Plugins view combines installed metadata with each plugin's live MCP state; Settings opens only from the top-bar gear.
- Agent is a shell-owned durable conversation workspace. Its left rail lists locally persisted conversations with search, new-conversation, and confirmed deletion actions. MCP catalog/scope/context controls are intentionally absent: the provider starts from bounded shell-owned meta-tools and can discover or start servers, request one concrete tool schema, and access prompts/resources through `mcp_context`; all execution remains brokered by the shell. Older transcript context is compacted into a durable checkpoint while recent user turns remain raw. Failed turns keep the user message and expose an explicit retry action. The composer streams text into one pending bubble, exposes a real Stop action backed by runtime cancellation, and accepts up to four bounded image/PDF attachments. Only the completed response is durable.
- Provider configuration lives in the dedicated **AI Providers** view. Each configured provider card exposes details and confirmed deletion; deleting a connection removes its encrypted credential and imported models, clears a stale active model, and removes its live runtime adapter. The detail surface supports editing the connection, importing its bounded `/models` catalog, and inspecting model context, modality, tool, and reasoning metadata. A default model is optional. The Agent composer searches the combined catalog across enabled providers and selects effort from the levels advertised by each model. Agent runtime routing, streaming, and vision gates are persisted and hot-reloaded from Settings. Credentials are stored only by the Electron main process through OS-backed `safeStorage`; the renderer receives only masked availability.
- MCP capability adoption follows [`mcp-capability-policy.md`](./architecture/mcp-capability-policy.md): tools, prompts, resources, and completion are the stable implementation track; deferred capabilities remain documented for operator and agent knowledge but are not advertised as supported.
- Each installed plugin has a shell-owned **Start MCP when NusaShell opens** preference in its detail sidebar. The preference persists with installed metadata; startup attempts opted-in MCPs independently and logs failures without blocking the launcher.
- Logs is a shell-owned live tail for renderer, Electron, IPC, backend, and MCP output. It retains at most 1,000 entries and must redact credential-like values before rendering.

### 4.2 Window Management

Each plugin opens as:

- **Panel** (default): tab/pane inside the main shell window - good for light plugins (notes, calculator)
- **Fullscreen**: takes the whole shell viewport - good for complex plugins (design tools, dashboards)
- **Widget**: small, dockable in a sidebar - good for always-visible plugins (mini chat, clock)

Mode comes from the plugin manifest (`ui.window.mode`); users can override per plugin.

### 4.3 Multi-window / Multi-tab

To support several plugins open at once (like a real desktop):

- Top tab bar; each tab = one plugin window instance
- Each tab has its own iframe + bridge toward that plugin’s MCP client
- Switching tabs does **not** kill the MCP process (Plugin Manager owns the process, independent of UI visibility)

---

## 5. MCP Transport Comparison for Plugins

| Transport | Best when | Notes |
| --- | --- | --- |
| `stdio` | The plugin MCP server runs as a local child process | Simplest; shell fully owns lifecycle (spawn/kill) |
| `sse` / `http` | The MCP server already exists as a remote service (plugin is mostly a UI wrapper) | No process to spawn; connect to a URL. Uninstall only removes the UI package |

**Recommendation:** support **both** in the manifest schema (`mcp.transport`) from
the start. Many real-world plugins will wrap an existing hosted MCP server rather
than shipping a new server from scratch.

---

## 6. Decision Summary & Next Steps

| Decision | Initial choice |
| --- | --- |
| Shell runtime | Electron + TypeScript (velocity) → migrate toward Tauri once stable |
| Backend shape | Clean Architecture monorepo; details in [`backend-structure.md`](./backend-structure.md) |
| Host ↔ backend | WebSocket (`ws`) as client transport; not an internal bus |
| Plugin UI ↔ host | iframe + `postMessage` / `window.shell.callTool` |
| Plugin bundling | One folder = manifest + `ui/` + `mcp/` |
| MCP connect | child_process (stdio) or existing remote (sse/http) - schema supports both |
| Installed metadata | filesystem/JSON early → SQLite in the monorepo MVP |
| Live runtime state | `PluginRuntimeManager` in-memory (not duplicated in DB/renderer/gateway) |
| Security | **deferred** - next phase as an additive layer, not built in from the start |

**Status & next steps:**

1. ~~Minimal shell PoC~~ - already in [`docs/PoC/`](./PoC/) (launcher + Notes + stdio bridge)
2. Scaffold the target monorepo (`apps/` + `packages/`) per [`backend-structure.md`](./backend-structure.md) §2 / §18
3. Finalize the manifest schema (JSON Schema / Zod) + `validate-manifest` script
4. Swap PoC hand-rolled JSON-RPC → official `@modelcontextprotocol/sdk`
5. Exercise `keepAliveOnClose` + idle suspend on one real background case
6. Only then enter the security phase: iframe sandboxing, permission dialogs, process isolation
