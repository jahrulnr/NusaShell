# MCP servers

MCP (Model Context Protocol) servers expose tools to the agent over stdio:
the shell spawns the configured command and speaks JSON-RPC over stdin and
stdout.

## Adding a server

A server needs a name, a command, arguments and optional environment
entries (`KEY=VALUE`). Example:

    name:     files
    command:  npx
    args:     -y @modelcontextprotocol/server-filesystem /path/to/dir

Servers connect lazily: the first tool listing or `mcp_call` spawns the
process. **Test** connects immediately and lists the tools.

## Tool exposure

Every tool of an enabled server becomes an agent tool named
`mcp__<server>__<tool>`. Tool schemas come from the server's own
`tools/list` response.

## Storage

Server definitions live in `mcp-servers.json` (JSON, non-credential).
Environment values may contain secrets by design of the personal shell; keep
the data directory private.
