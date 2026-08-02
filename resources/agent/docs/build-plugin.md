# Build a NusaShell plugin

A plugin is one folder containing a manifest and an MCP server. A UI is
optional, so choose the smallest shape that fits the capability.

## Choose a shape

| Shape | Folder contents | User experience |
| --- | --- | --- |
| Headless MCP-only | `manifest.json` + `mcp/` | Agent and Plugins view only; no Home tile or plugin window |
| Windowed plugin | `manifest.json` + `ui/` + `mcp/` | Home tile opens the UI; the agent can also call its MCP tools |

Use headless for background, automation, or agent-only capabilities. Use a
windowed plugin when users need a dashboard, form, or other visual surface.

## Minimal layout

```text
my-plugin/
├── manifest.json
├── mcp/
│   └── server.cjs
└── ui/                 # optional for a windowed plugin
    └── index.html
```

`icon` is required for both shapes; emoji or text is valid. A windowed manifest
must declare `ui.entry`, for example `ui/index.html`. The MCP server can use the
stdio shape used by the built-ins:

```json
{
  "id": "example.my-plugin",
  "name": "My Plugin",
  "version": "0.1.0",
  "icon": "N",
  "mcp": {
    "transport": "stdio",
    "command": "node",
    "args": ["mcp/server.cjs"],
    "env": {},
    "autostart": false
  }
}
```

For a windowed plugin, add:

```json
"ui": { "entry": "ui/index.html" }
```

The UI must call tools through `window.shell.callTool(toolName, args)`; it must
not speak raw MCP or connect directly to the backend. The shell remains the
broker between the iframe and MCP server.

## Examples and installation

Look at the built-in source under `plugins/notes/`, `plugins/files/`,
`plugins/mail/`, and `plugins/terminal/`. They show manifests, stdio servers,
UI bridges, and packaging conventions. The repository skill
`.agents/skills/build-nusashell-plugin/SKILL.md` has the fuller authoring and
verification workflow.

For Cursor/repository development, place the plugin under the repository
`plugins/` tree. For the in-app agent, read the `mcp-creator` skill and write
only under `{userData}/plugins/{folder}/`, then call `mcp_register` for
interactive validation and admission. Humans can still use the Add Plugin UI
for folders/archives. The installer validates `manifest.json`, declared UI
entry files, and local icon files; `mcp_register` never accepts arbitrary repo,
URL, or download paths.

For an installed plugin's exact directory, ask `mcp_list` and use its
`installPath`; do not guess from Linux paths when running on macOS or Windows.

## Safety boundary

Only install plugins you trust and understand. NusaShell brokers lifecycle and
tool calls but does not audit or certify third-party MCP behavior. See
[`security.md`](security.md) before enabling a plugin, especially one that can
write files, access credentials, or make network requests.

If the conversation workspace is the NusaShell repository, an agent may scaffold
these files there after confirming the intended plugin name and location. In
another workspace, give the folder recipe and ask where the user wants files
written.

Related: [`plugins.md`](plugins.md), [`data-locations.md`](data-locations.md),
and [`security.md`](security.md).
