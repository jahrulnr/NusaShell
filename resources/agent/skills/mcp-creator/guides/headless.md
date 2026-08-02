# Headless plugin

Use headless for agent, automation, or background capability without a visual
surface.

```text
example.plugin/
├── manifest.json
└── mcp/
    ├── server.cjs
    ├── tools.js
    ├── prompts.js
    └── errors.js
```

Omit `ui` from the manifest. Set `autostart` only when startup readiness is
needed. Use a `howto` or `workflow` MCP prompt for domain/multi-step behavior;
for native-like tools, include root/cwd, limits, and destructive-operation
constraints. Keep prompt text short and point to live schemas.
