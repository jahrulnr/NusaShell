# Plugins (MCP servers + MCP+UI plugins)

A **plugin** is the single concept for anything that exposes MCP tools:
a manual MCP server or an installed plugin from the catalog
(MCP-only or MCP + UI). The shell connects to the plugin's MCP server
over three transports: **stdio** (spawns the command and speaks
JSON-RPC over stdin/stdout), **SSE**, and **HTTP** (Streamable HTTP —
both connect to a remote endpoint URL, with optional HTTP headers for
auth such as `Authorization: Bearer <token>`).

MCP plugin tools are not advertised in `tools[]` — see Tool exposure below
for the discovery and execution contract.

## Adding a plugin manually

A manual MCP-server plugin needs a name plus transport-specific
connection fields. For **stdio**: a command, arguments, and optional
environment entries (`KEY=VALUE`). Example:

    name:      files
    transport: stdio
    command:   npx
    args:      -y @modelcontextprotocol/server-filesystem /path/to/dir

For **SSE** or **HTTP** (Streamable HTTP): a URL and optional headers.
Example (agent tool):

    mcp_server_add(name="context7", transport="http", url="https://mcp.context7.com/mcp",
                   headers={"Authorization": "Bearer <token>"})

stdio servers require `command`; SSE/HTTP servers require an
`http(s)` URL; other values are rejected at save time. When editing an
existing server via `plugin.save` without a `transport` field, its
current transport is kept — remote servers are never silently degraded
back to stdio.

It is stored as `plugins/<id>/manifest.json` exactly like a catalog
installed plugin. Plugins with `mcp.autostart` (the Plugins drawer toggle)
are connected when the Go process starts, so CI workflows and the agent
toolbox can use those tools immediately. Other plugins stay lazy: the first
tool listing or **Start** (`plugin.test`) opens the connection. **Stop**
(`plugin.stop`) drops the cached connection and **Restart** stops then starts.

## Tool exposure

MCP plugin tools are not advertised in the tool list sent to the provider.
You will not see them in `tools[]`. Use the universal `mcp_search` +
`mcp_call` pair to discover and execute — this works on every provider and
keeps the tool list stable. Do NOT write tool calls as text in your reply
and do NOT call `mcp__<server>__<tool>` names — they are not callable;
`mcp_call` with a `ref` is the only execution path. `tool_list` returns
`ref`s too, so every discovery flow ends in `mcp_call`. Tool schemas come
from the server's own `tools/list` response. `mcp_search` accepts an
optional `server` (plugin id); when omitted, it searches across ALL
running MCP servers, so the agent can find a tool without knowing which
server hosts it. `mcp_search` returns a `ref` plus the full tool
definition (name, server, description, parameters) so the agent can call
the tool directly via `mcp_call` without a follow-up `tool_schema`
round-trip — this is the preferred discovery path. `tool_list` (list ALL
tools of a server, no query) complements `mcp_search` (query-based,
ranked) — use `mcp_search` to find a specific tool, `tool_list` to see
everything a server offers.

### Server→client notifications

Plugins may push MCP notifications (e.g. `notifications/message`) to the host
to signal "something happened" without being polled. The host bridges them
into CI events (`infrastructure/mcpclient/notify.go`); see
Automation → Plugin push events for the `when:` trigger contract. As an
agent you do not act on notifications directly — they are consumed by the
CI engine, which starts the matching workflow.

### Idle plugin enable workflow

An idle plugin (listed in `mcp_list` with `running: false`) must be enabled
before its tools can be called.

Good example:

    mcp_list()                                       # → {"name":"Files","id":"nusashell.files","running":false,"tools":0}
    mcp_enable(id="nusashell.files")                 # → {"status":"enabled","tools":3} (status + count only)
    mcp_search(query="read file")                    # → {"ref":"nusashell.files:read","name":"read","server":"nusashell.files","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}} (JSONL)
    mcp_call(ref="nusashell.files:read", arguments_json="{\"path\": \"/home/user/a.txt\"}")  # executes the tool

`mcp_enable` returns only status + tool count — it does NOT dump tool
definitions. After `mcp_enable`, call `mcp_search` to discover the tools
(returns `ref` + full definitions with parameters), then `mcp_call` to
execute. Use `tool_schema` only when you need the exact argument shape (field names,
types, required fields) for a single tool and don't already have it from
`mcp_search`.
If the plugin is already connected, `mcp_enable` returns
`status: already_enabled` without reconnecting — do NOT call
`mcp_enable` again for the same plugin; use `mcp_search` or call the
tools directly.

`mcp_search`, `tool_list`, and `tool_schema` accept the plugin id only
(e.g. "nusashell.files") — the same value `mcp_list` returns in its `id`
field and the same value used as the prefix in tool refs
(`<plugin-id>:<tool>`). Display names and "plugin:"-prefixed server ids
are NOT accepted; this keeps tool discovery unambiguous across thousands
of MCP tools with similar display names.

Bad example — guessing a tool name without discovery:

    mcp__files__read({path: "/home/user/a.txt"})  # not in tools[], not callable

Bad example — writing the tool call as text instead of using `mcp_call`:

    # WRONG — this is text, the runtime will NOT execute it:
    to=nusashell.files:read code:.json
    {"path":"/home/user/a.txt"}

    # RIGHT — use mcp_call with the ref from mcp_search:
    mcp_call(ref="nusashell.files:read", arguments_json="{\"path\": \"/home/user/a.txt\"}")

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
