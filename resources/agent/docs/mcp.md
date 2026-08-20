# Plugins (MCP servers + MCP+UI plugins)

A **plugin** is the single concept for anything that exposes MCP tools:
a manual MCP server (stdio) or an installed plugin from the catalog
(MCP-only or MCP + UI). The shell spawns the plugin's MCP command and
speaks JSON-RPC over stdin/stdout.

MCP plugin tools are NOT advertised in the tool list sent to the provider —
the tool list must stay stable for the lifetime of a conversation so the
provider prompt cache (OpenAI / Claude) is not invalidated. You will not
see MCP tools in `tools[]`. Use the universal `mcp_search` + `mcp_call`
pair to discover and execute MCP tools — this works on every provider and
keeps the tool list stable. Do NOT write tool calls as text in your reply
and do NOT guess `mcp__<server>__<tool>` names (they are not in `tools[]`
and may not be callable on your provider). The legacy `tool_list`,
`tool_search`, and `tool_schema` tools still work for inspection but
return text the model must then act on — prefer `mcp_search` + `mcp_call`
for execution.

## Adding a plugin manually

A manual MCP-server plugin needs a name, a command, arguments and optional
environment entries (`KEY=VALUE`). Example:

    name:     files
    command:  npx
    args:     -y @modelcontextprotocol/server-filesystem /path/to/dir

It is stored as `plugins/<id>/manifest.json` exactly like a catalog
installed plugin. Plugins with `mcp.autostart` (the Plugins drawer toggle)
are connected when the Go process starts, so automations and the agent
toolbox can use those tools immediately. Other plugins stay lazy: the first
tool listing or **Start** (`plugin.test`) spawns the process. **Stop**
(`plugin.stop`) drops the cached connection and **Restart** stops then starts.

## Tool exposure

MCP plugin tools are not advertised in the tool list sent to the provider.
You will not see them in `tools[]`. Use the universal `mcp_search` +
`mcp_call` pair to discover and execute — this works on every provider and
keeps the tool list stable. Do NOT write tool calls as text in your reply
and do NOT guess `mcp__<server>__<tool>` names. The legacy `tool_list`,
`tool_search`, and `tool_schema` tools still work for inspection but
return text the model must then act on — prefer `mcp_search` + `mcp_call`
for execution. Tool schemas come from the server's own `tools/list`
response. `mcp_search` accepts an optional `server`; when omitted, it
searches across ALL running MCP servers, so the agent can find a tool
without knowing which server hosts it. `mcp_search` returns a `ref` plus
the full tool definition (name, server, description, parameters) so the
agent can call the tool directly via `mcp_call` without a follow-up
`tool_schema` round-trip — this is the preferred discovery path.

### Idle plugin enable workflow

An idle plugin (listed in `mcp_list` with `running: false`) must be enabled
before its tools can be called.

Good example:

    mcp_list()                                       # → {"name":"Files","id":"nusashell.files","running":false,"tools":0}
    mcp_enable(id="nusashell.files")                 # → {"status":"enabled","tools":3} (status + count only)
    mcp_search(query="read file")                    # → {"ref":"Files:read","name":"read","server":"Files","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}} (JSONL)
    mcp_call(ref="Files:read", arguments={"path": "/home/user/a.txt"})  # executes the tool

`mcp_enable` returns only status + tool count — it does NOT dump tool
definitions. After `mcp_enable`, call `mcp_search` to discover the tools
(returns `ref` + full definitions with parameters), then `mcp_call` to
execute. Use `tool_schema` only when you need the exact argument shape
for a single tool and don't already have it from `mcp_search`.
If the plugin is already connected, `mcp_enable` returns
`status: already_enabled` without reconnecting — do NOT call
`mcp_enable` again for the same plugin; use `mcp_search` or call the
tools directly.

`mcp_search` and `tool_schema` accept the server name (e.g. "Files"),
the plugin id (e.g. "nusashell.files"), or the MCP server id (e.g.
"plugin:nusashell.files") — use whichever you have.

Use `tool_schema` when you need the exact argument shape (field names,
types, required fields) before calling an MCP tool. It returns the
schema as readable JSON in the body.

Bad example — guessing a tool name without discovery:

    mcp__files__read({path: "/home/user/a.txt"})  # not in tools[], may not be callable on your provider

Bad example — writing the tool call as text instead of using `mcp_call`:

    # WRONG — this is text, the runtime will NOT execute it:
    to=mcp__Files__read code:.json
    {"path":"/home/user/a.txt"}

    # RIGHT — use mcp_call with the ref from mcp_search:
    mcp_call(ref="Files:read", arguments={"path": "/home/user/a.txt"})

The Plugins view is the single catalog for all plugins: manual MCP
servers, MCP-only plugins, and MCP + UI plugins. Select an entry to test,
stop, restart, edit/delete (manual MCP entries), uninstall, or open the UI
for an MCP + UI plugin. RPC methods are `plugin.list`, `plugin.save`,
`plugin.test`, `plugin.stop`, `plugin.delete`, `plugin.uninstall`,
`plugin.catalog`, `plugin.install`.

## Installing plugins

The Plugins view has an **Install plugin** dialog with three sources:

- **Catalog** — official first-party plugins from the NusaShell catalog
  (`plugin.catalog`). Each entry shows name, version, icon and description;
  search filters the list as you type. Installing replaces an existing
  install of the same plugin.
- **GitHub** — install from a repository URL or `owner/repo` shorthand.
  Optional *subdirectory* targets a plugin inside a monorepo; optional *ref*
  pins a branch or tag (defaults to the repository default branch).
- **Upload ZIP** — install a plugin archive from disk. The archive may wrap
  the plugin in a single top-level directory or place `manifest.json` at the
  root; it must contain exactly one `manifest.json`.

Installing downloads to a temporary staging directory, validates the
manifest, and copies the plugin into `plugins/<id>/` under the data
directory. The existing plugin with the same id is replaced. The mail plugin
is not available for install in the Go port. Plugins whose archive includes
`node_modules/` keep it; `.git` directories and symlinks are skipped during
the copy.

## Plugin icons

Manifest icons accept three forms, resolved the same way as the Electron
shell:

- **Text / emoji** (e.g. `"📝"`, `"N"`) — displayed as-is.
- **HTTP(S) URL** — loaded directly by the browser.
- **File icon** (`"file://icon.png"`, `"file:///abs/icon.png"`,
  `"./icon.png"`, `"icon.png"`) — resolved against the plugin directory,
  validated as a PNG no larger than 5 MiB that stays inside the plugin
  folder, and embedded as a `data:image/png;base64,...` URL. Catalog entries
  fetch the same file from the plugin repository. Icons that cannot be
  resolved fall back to a 🧩 placeholder.

## Storage

All plugin definitions live under `plugins/<id>/manifest.json` in the data
directory (JSON, non-credential). There is no separate MCP server store —
a manual MCP server is just a plugin manifest with an `mcp` block and no
`ui` block. Environment values may contain secrets by design of the
personal shell; keep the data directory private.
