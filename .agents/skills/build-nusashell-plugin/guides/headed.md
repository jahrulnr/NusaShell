# Headed Plugin Guide

Use this shape when the capability needs a dedicated Home tile and visual workspace.

## Package shape

```text
plugins/example-tool/
├── manifest.json
├── package.json
├── icon.png
├── ui/
│   ├── index.html
│   ├── app.js
│   └── style.css
├── mcp/
└── tests/
```

## Minimal stdio manifest

```json
{
  "id": "example.tool",
  "name": "Example Tool",
  "version": "1.0.0",
  "icon": "file://icon.png",
  "ui": {
    "entry": "ui/index.html",
    "window": {
      "mode": "panel",
      "defaultSize": { "width": 720, "height": 480 },
      "resizable": true
    }
  },
  "mcp": {
    "transport": "stdio",
    "command": "node",
    "args": ["mcp/server.cjs"],
    "env": {},
    "autostart": false,
    "keepAliveOnClose": false
  },
  "dependencies": { "shell": ">=0.1.1" }
}
```

Keep `ui.entry` plugin-relative and ensure it points to a real contained file. Choose the real shell compatibility floor.

## Bridge

The host appends `pluginId` to the UI URL and exposes an isolated preload API:

```js
const pluginId = new URLSearchParams(location.search).get("pluginId") || "";
const result = await window.shell.callTool(pluginId, "example_list", {});
```

Use the current three-argument preload contract from `apps/desktop/src/preload/index.ts`. Never call MCP, the backend WebSocket, or the child process directly from `ui/`.

The host throws for MCP `isError`, unwraps `structuredContent`, and falls back to `content`. Prefer plain structured results. Add legacy result normalization only when compatibility is explicitly required.

## Window and lifecycle

| Mode | Working posture | Current fallback size |
| --- | --- | --- |
| `panel` | Focused utility/task | 720×480 |
| `fullscreen` | Dense workspace | 1200×800 |
| `widget` | Compact glance/action | 420×360 |

The desktop clamps width to 400–1920 and height to 300–1200, then fits to the display. It starts MCP before loading the window. Use lazy startup by default; use `autostart` only for pre-window work. Set `keepAliveOnClose` only for genuine background behavior that users can understand.

## UI quality and safety

- Use the frontend-design skill to ground the visual system in the plugin's subject rather than a generic dashboard.
- Model initial, loading, success, empty, validation error, operation error, retry, and destructive-confirmation states.
- Use semantic controls, labels, keyboard navigation, visible focus, contrast, and reduced-motion support.
- Sanitize rendered HTML/markdown with a proven policy covering elements, attributes, protocols, and URLs; simple script-tag stripping is insufficient.
- Keep bridge calls behind small functions and state/render transformations testable outside the browser.

## Headed verification

1. Test MCP service, descriptors, dispatch, and safe errors independently.
2. Test UI state with success, empty, rejection, and hostile-content fixtures.
3. Mock `window.shell` and assert plugin id, tool name, args, result handling, and visible failures.
4. Confirm Home tile, Plugins entry, window sizing, startup-before-load, close/reopen, and running badge semantics.
5. Update relevant UI knowledge docs when behavior/visual contracts change, following `AGENTS.md`.
