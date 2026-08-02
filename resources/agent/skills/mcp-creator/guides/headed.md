# Headed plugin

Use headed only when users need a Home tile and dedicated visual workspace.

```text
example.plugin/
├── manifest.json
├── ui/index.html
└── mcp/
    ├── server.cjs
    ├── tools.js
    └── prompts.js
```

Declare a contained `ui.entry`, keep UI calls on `window.shell.callTool`, and
never connect the iframe directly to MCP or WebSocket. The MCP side still needs
plugin-owned howto/workflow prompts: strongly required for domain flows and
recommended for native-like tools. Register the prompt capability and verify it
through `mcp_context` after `mcp_enable`.
