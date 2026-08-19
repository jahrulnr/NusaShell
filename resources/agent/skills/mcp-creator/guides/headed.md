# Headed plugin

Use headed only when users need a Home tile and dedicated visual workspace.

```text
example.plugin/
├── manifest.json
├── ui/index.html
└── mcp/
    ├── server.cjs
    └── tools.js
```

Declare a contained `ui.entry`, keep UI calls on `window.shell.callTool`, and
never connect the iframe directly to MCP or WebSocket. Keep any required tool
ordering and workspace constraints in bounded MCP tool descriptions so the
in-app agent receives them through live discovery.
