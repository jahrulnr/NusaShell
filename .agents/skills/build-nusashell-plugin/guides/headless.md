# Headless Plugin Guide

Use this shape for MCP capabilities without a dedicated visual surface.

## Package shape

```text
plugins/example-indexer/
├── manifest.json
├── package.json
├── icon.png              # optional asset; manifest icon is required
├── mcp/
└── tests/
```

Do not create `ui/`. Omit `ui` completely from the manifest; never use an empty object, placeholder entry, or hidden page.

## Minimal stdio manifest

```json
{
  "id": "example.indexer",
  "name": "Indexer",
  "version": "1.0.0",
  "icon": "🧩",
  "mcp": {
    "transport": "stdio",
    "command": "node",
    "args": ["mcp/server.cjs"],
    "env": {},
    "autostart": true,
    "keepAliveOnClose": false
  },
  "dependencies": { "shell": ">=0.1.1" }
}
```

Choose the real shell compatibility floor; do not copy the example mechanically. For remote MCP, use `http` or `sse` with `url` and omit process-only assumptions.

## Lifecycle

- Set `autostart: true` only when the capability must be ready at shell startup. Otherwise require explicit enablement from Plugins or agent `mcp_enable`.
- Treat autostart as process readiness, not workspace readiness. The active conversation workspace may arrive later through MCP Roots/synchronization; wait for a valid root and never index the plugin install `cwd` as a fallback.
- Keep `keepAliveOnClose: false`; a headless plugin has no window whose close event should control its runtime.
- Expose useful health/status through lifecycle state, logs, tools, prompts, or resources instead of inventing a UI.
- Expect management through Plugins and agent `mcp_*` tools. The plugin must not appear on Home and must never open a BrowserWindow.

## Headless verification

1. Confirm installer/sync accepts the manifest without `ui`.
2. Confirm the plugin appears in Plugins and agent MCP discovery but not Home.
3. Exercise enable → tool search/list/schema → call → disable.
4. Test autostart only when declared; test clean stop and crash recovery for stdio.
5. Confirm scheduled/agent callers receive bounded structured results without requiring renderer state.
