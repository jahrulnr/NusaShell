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
`http(s)` URL; other values are rejected at save time.

Manual servers are stored like installed plugins (see Storage below). The
Plugins drawer's **Auto start** toggle connects them at startup, so
automation workflows and the agent toolbox can use those tools immediately.
Other plugins stay lazy: the first tool listing or **Start** opens the
connection; **Stop** drops it and **Restart** stops then starts.

## Tool exposure

MCP plugin tools are not advertised in the tool list sent to the provider.
You will not see them in `tools[]`. Use the universal `mcp_search` plus
`mcp_call` pair to discover and execute — this works on every provider and
keeps the tool list stable. Do not write tool calls as text in your reply and
do not call `mcp__<server>__<tool>` names — they are not callable;
`mcp_call` with a `ref` is the only execution path. `mcp_search` accepts an
optional `server` (plugin id); when omitted, it searches across all running
MCP servers. Both `mcp_search` and `tool_list` return compact refs and omit
input schemas. Use `tool_schema` for the exact argument shape when needed,
then call the returned ref with `mcp_call`.

Discovery output is size-bounded. `tool_list`, `mcp_search`, and `tool_schema`
cap the in-band payload at ~32KiB; an oversized catalog or schema spills the
complete JSONL to the platform temp dir (`nusashell/`) and the result carries
`truncated: true`, `overflow_path`, `overflow_bytes`, and `next_offset_bytes` —
continue with `file_read` from that offset instead of re-running the discovery
call. Spill files are swept after 24 hours.

`mcp_search` also reports `count` (matches returned), `total` (every match
before the limit), and `limit`. When `count < total` the list was cut by the
default limit (20) or by an explicit one — raise `limit` or narrow the query
instead of concluding the catalog only holds those matches.

Good example — an MCP-heavy catalog cut by the limit and by the byte budget:

    mcp_search(query="issue")                    # count: 20 · total: 137 · limit: 20 → 117 matches cut
    mcp_search(query="issue", limit=137)         # raise the limit to reach them all
      → truncated: true, overflow_path: /tmp/nusashell/mcp_search-ab12.txt, next_offset_bytes: 32768
    file_read(path="/tmp/nusashell/mcp_search-ab12.txt", offset_bytes=32768)

Bad example — treating a limit cut as the whole catalog:

    mcp_search(query="issue")   # count: 20 · total: 137 · limit: 20
    # WRONG: "this server only has 20 issue tools" — the limit hid 117 matches.
    # RIGHT: re-query with limit=137, or search per server with a narrower query.

### Server→client notifications

Plugins that publish business facts must use the NusaShell-specific notification
method `notifications/nusashell/event`. The host translates its `params` into a
normalized automation event; the agent does not handle the notification directly.

The generic envelope is:

```json
{
  "schema_version": 1,
  "event_id": "github-delivery-123",
  "type": "github.pull_request",
  "occurred_at": "2026-09-08T10:00:00Z",
  "subject": "owner/repo#42",
  "attributes": {
    "action": "opened",
    "repository": "owner/repo"
  },
  "data": {
    "pull_request_number": 42,
    "head_sha": "abc123"
  }
}
```

`schema_version`, `event_id`, and `type` are required. `occurred_at` must be an
RFC3339 timestamp; `subject`, `attributes`, and `data` are optional. The host
assigns `source` from the connected MCP server ID, not from plugin params, and
namespaces the stable normalized ID as `mcp:<server-id>:<event_id>`. The raw
publisher ID is also available as the host-managed `event_id` attribute.
Omitted `attributes` and `data` become empty objects.

The adapter rejects unsupported schema versions, missing or malformed identity,
unknown top-level fields, non-object attributes, invalid JSON data, and
oversized values. The current limits are 4 KiB per string field, 256 KiB for
serialized attributes, and 8 MiB for serialized data. Keep event payloads as
facts, not instructions, and put provider-specific fields under `attributes` or
`data`.

Good publisher payload:

```text
method: notifications/nusashell/event
params: {schema_version: 1, event_id: "trade-20260908-001", type: "trading.price_alert", attributes: {symbol: "BTCUSD", severity: "high"}, data: {price: 64123.5}}
```

Bad publisher payload:

```text
method: notifications/message
params: {type: "github.pull_request", action: "opened"}
```

`notifications/message` is the MCP logging notification method, not a generic
business-event envelope. NusaShell accepts its message-shaped payload only as
a legacy messaging bridge for older messaging plugins: it requires a matching
connected server/plugin ID, nonempty `chat_id` and `message_id`, and an
explicit `from_me: false`, and it must not be used for trading, GitHub, or new
publishers. Logging-shaped `notifications/message` payloads are ignored. New
integrations target `notifications/nusashell/event`.

See Automation → Event-driven workflows for trigger and side-effect guidance.
As an agent you do not act on notifications directly — the automation engine
consumes accepted events and starts matching workflows.

### Idle plugin enable workflow

An idle plugin (listed in `mcp_list` with `running: false`) must be enabled
before its tools can be called.

Good example:

    mcp_list()                                       # → {"name":"Files","id":"nusashell.files","running":false,"tools":0}
    mcp_enable(id="nusashell.files")                 # → {"status":"enabled","tools":3} (status + count only)
    mcp_search(query="read file")                    # → {"ref":"nusashell.files:read","name":"read","server":"nusashell.files","description":"Read a text file..."} (JSONL)
    tool_schema(server="nusashell.files", tool="read")  # full input schema
    mcp_call(ref="nusashell.files:read", arguments_json={"path": "/home/user/a.txt"})  # executes the tool

`arguments_json` is a JSON **object** matching the tool's parameters schema
(canonical form). Omit it for parameterless tools; a JSON-encoded string
form is also accepted. If a tool declares required fields and the call
arrives with empty arguments, the runtime rejects it with `MISSING_ARGS:
<ref> requires [<fields>]; load tool_schema and retry with arguments_json
as a JSON object`.

Each `mcp_call` is bounded by `timeout_ms` — default **30000** (30s),
max **3600000** (1h). A stdio server that hangs holds the call only up to
this bound; when it fires, the call fails with a timeout error even though
the tool may still be running server-side. Raise `timeout_ms` only for
operations that are legitimately slow (deploys, long renders, batch jobs).

    # a quick lookup — the 30s default is fine:
    mcp_call(ref="nusashell.files:read", arguments_json={"path": "/home/user/a.txt"})
    # a known-slow operation — raise the bound explicitly:
    mcp_call(ref="ci.server:deploy", arguments_json={"env": "prod"}, timeout_ms=600000)

On Responses and Codex, supply the same tool-specific object fields returned
by `tool_schema`; an empty object is only appropriate when the tool needs no
arguments.

    # GOOD — include the discovered tool's required field:
    mcp_call(ref="nusashell.files:read", arguments_json={"path": "/home/user/a.txt"})
    # BAD — the read tool requires path:
    mcp_call(ref="nusashell.files:read", arguments_json={})

`mcp_enable` returns only status + tool count — it does NOT dump tool
definitions. After `mcp_enable`, call `mcp_search` or `tool_list` to discover
the tools, call `tool_schema` when an argument shape is unclear, then
`mcp_call` to execute.
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
    mcp_call(ref="nusashell.files:read", arguments_json={"path": "/home/user/a.txt"})

The Plugins view is the single catalog for all plugins: manual MCP
servers, MCP-only plugins, and MCP + UI plugins. Select an entry to test,
stop, restart, edit/delete (manual MCP entries), uninstall, or open the UI
for an MCP + UI plugin.

## Installing plugins

The Plugins view has an **Install plugin** dialog with three sources:

- **Catalog** — official first-party plugins from the NusaShell catalog.
  Each entry shows name, version, icon and description; search filters the
  list as you type. Installing replaces an existing install of the same
  plugin.
- **GitHub** — install from a repository URL or `owner/repo` shorthand.
  Optional *subdirectory* targets a plugin inside a monorepo; optional *ref*
  pins a branch or tag (defaults to the repository default branch).
- **Upload ZIP** — install a plugin archive from disk. The archive may wrap
  the plugin in a single top-level directory or place `manifest.json` at the
  root; it must contain exactly one `manifest.json`.

Installing validates the archive's manifest and replaces an existing plugin
with the same id. The mail plugin cannot be installed.

## Agent plugin management

The agent-side management tools complement the Plugins view:

- `mcp_install(source="catalog", id="<catalog-id>")` installs a catalog
  plugin; `mcp_install(source="github", url="owner/repo")` installs from
  GitHub (optional `subdir` and `ref`). After install, call `mcp_enable`
  with the returned plugin id.
- `mcp_register(source)` registers a plugin from an absolute local folder
  containing `manifest.json` and replaces an existing id — check `mcp_list`
  and ask the user before replacing. For a generic MCP server with no
  manifest, use `mcp_server_add` instead.
- `mcp_disable(id)` stops the plugin's connection; the definition stays
  installed. `mcp_unregister(id)` removes the plugin and deletes its
  installed folder — ask the user for confirmation first, and prefer
  `mcp_disable` when the plugin only needs to stop.

Good example — install then enable:

    mcp_install(source="catalog", id="nusashell.terminal")   # → {"status":"installed","id":"nusashell.terminal"}
    mcp_enable(id="nusashell.terminal")                    # → {"status":"enabled","tools":3}
    mcp_search(query="run command")                        # discover the new tools

### Usage contracts

A plugin may declare a usage contract — best-practice rules plus a state &
side-effect disclosure. `mcp_list` marks these `contract: true`. Read one
with `contract_read(id="<plugin-id>")` (`id="all"` reads every declaring
plugin). Enforcement follows Settings → **Plugin usage contracts**
(`plugin_contract_mode`): `off` never nudges, `hint` (default) appends an
advisory note to `mcp_call` results until the contract is read, and
`require` blocks `mcp_call` with `CONTRACT_REQUIRED` until `contract_read`
has run in the conversation — read it, then retry the call.

## Plugin icons

Manifest icons accept three forms:

- **Text / emoji** (e.g. `"📝"`, `"N"`) — displayed as-is.
- **HTTP(S) URL** — loaded directly by the browser.
- **File icon** (`"file://icon.png"`, `"file:///abs/icon.png"`,
  `"./icon.png"`, `"icon.png"`) — resolved against the plugin directory,
  validated as a PNG no larger than 5 MiB that stays inside the plugin
  folder, and embedded as a `data:image/png;base64,...` URL. Catalog entries
  fetch the same file from the plugin repository. Icons that cannot be
  resolved fall back to a 🧩 placeholder.

## Storage

Plugin definitions live under `plugins/<id>/` in the data directory — see
Data locations. Environment values may contain secrets; keep the data
directory private.
