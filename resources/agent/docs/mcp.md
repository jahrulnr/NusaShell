# Plugins (MCP servers + MCP+UI plugins)

A **plugin** is the single concept for anything that exposes MCP tools to
the agent: a manual MCP server (stdio) or an installed plugin from the
catalog (MCP-only or MCP + UI). The shell spawns the plugin's MCP command
and speaks JSON-RPC over stdin/stdout; tools surface to the agent as
`mcp__<server>__<tool>`.

## Adding a plugin manually

A manual MCP-server plugin needs a name, a command, arguments and optional
environment entries (`KEY=VALUE`). Example:

    name:     files
    command:  npx
    args:     -y @modelcontextprotocol/server-filesystem /path/to/dir

It is stored as `plugins/<id>/manifest.json` exactly like a catalog
installed plugin. Plugins connect lazily: the first tool listing spawns the
process. **Start** (plugin.test) connects immediately and lists the tools;
**Stop** (plugin.stop) drops the cached connection and **Restart** stops
then starts.

## Tool exposure

Every tool of an enabled server becomes an agent tool named
`mcp__<server>__<tool>`. Tool schemas come from the server's own
`tools/list` response.

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
