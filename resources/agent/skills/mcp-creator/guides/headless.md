# Headless plugin

Use headless for agent, automation, or background capability without a visual
surface.

```text
example.plugin/
├── manifest.json
└── mcp/
    ├── server.cjs
    ├── tools.js
    └── errors.js
```

Omit `ui` from the manifest. Set `autostart` only when startup readiness is
needed. Put required ordering, root/cwd rules, limits, and destructive-operation
constraints in bounded tool descriptions so live discovery exposes them.
