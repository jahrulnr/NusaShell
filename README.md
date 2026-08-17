# NusaShell

NusaShell is a local, personal AI shell written in Go. It delivers the
NusaShell conversation experience in a single binary: an embedded native
JavaScript frontend, multi-conversation agent chat, provider adapters, MCP,
skills, memory, and documentation tools.

The interface follows the NusaShell Electron renderer closely, including the
conversation rail, workspace picker, context counter, attachment affordance,
streaming tool timeline, and expandable reasoning and tool output.

> This application has no authentication or rate limiting by design. Bind it
> to `127.0.0.1` or run it only on a trusted network.

## What it includes

- **Agent conversations** — streaming turns, stop/interrupt, automatic context
  compaction, provider prompt caching, context usage, file attachments, and a
  folder-backed workspace selected through the native picker.
- **Readable execution timeline** — reasoning is rendered as Markdown, and
  tool calls are collapsed by default. Expand a call only when needed; outputs
  longer than ten lines scroll inside their own panel instead of stretching the
  conversation.
- **Providers** — Messages, Responses, and Chat API formats for Anthropic,
  OpenAI-compatible services, DeepSeek, Ollama, LM Studio, vLLM, and similar
  endpoints. API keys are kept in a local SQLite credential store.
- **MCP, skills, memory, and docs** — stdio MCP tools are available to the
  agent as `mcp__<server>__<tool>`; reusable Markdown skills, durable memory,
  and local documentation search round out the workspace.
- **Local-first delivery** — Go serves the frontend assets embedded with
  `embed.FS`. Production use needs no Node runtime, `node_modules`, or frontend
  build step.

## Quick start

Requirements:

- Go 1.26.5 or newer
- Node.js 24 or newer only for the frontend E2E test
- A desktop folder-dialog provider on Linux (install `zenity` if no compatible
  provider is already available). Windows and macOS use their system dialogs.

```bash
git clone git@github.com:jahrulnr/NusaShell.git
cd NusaShell

# Keep the local UI private to this machine.
export NUSASHELL_HOST=127.0.0.1
make run
```

Open `http://127.0.0.1:9999`, configure a provider, then choose a folder from
the composer’s workspace button. The selected folder is the workspace for the
active conversation.

To run NusaShell from anywhere as a `nusashell` command (the Go app owns the
CLI name; it replaces any wrapper left by NusaShell-Desktop):

```bash
make install              # builds ./bin/nusashell and installs to ~/.local/bin
nusashell                 # starts the server (default http://127.0.0.1:9999)
```

Set `NUSASHELL_INSTALL_DIR` to install to a different directory.

## Development and verification

```bash
make fmt                 # format Go sources
make fmt-check           # report unformatted Go sources
go test ./...            # ordinary Go test run
go test -race ./...      # race-enabled test run
go vet ./...             # static analysis
go build ./...           # compile every package
make check               # fmt-check + race test + vet + build
```

The production frontend is plain browser JavaScript, but its smoke test uses
JSDOM. Install the development dependency once, then run the browser-facing
checks:

```bash
npm ci
node --test frontend/*.test.mjs
```

The E2E test starts the real Go server and exercises a representative flow
through RPC, WebSocket events, application services, and local persistence.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `NUSASHELL_HOST` | `127.0.0.1` | HTTP listen host |
| `NUSASHELL_PORT` | `9999` | HTTP listen port |
| `NUSASHELL_DATA_DIR` | platform config directory plus `nusashell` | Local data directory |
| `NUSASHELL_DEV` | unset | Serve `frontend/` directly from disk for development |

Use `NUSASHELL_DEV=1` when iterating on the UI. The server keeps the same
handler contract in development and production; only the source of static
assets changes.

## Project layout

| Path | Responsibility |
| --- | --- |
| `domain/` | Pure entities, value objects, and policies |
| `application/` | Use cases, ports, agent runner, and event bus |
| `contracts/` | Wire types, method roster, and JSON fixtures |
| `infrastructure/` | Local stores, provider adapters, MCP, tools, and docs |
| `transport/` | HTTP RPC, WebSocket, SSE, and static asset serving |
| `cmd/nusashell/` | Composition root, configuration, lifecycle, and entrypoint |
| `frontend/` | Embedded native JavaScript, CSS, HTML, and frontend tests |
| `testdata/` | Stable fixtures, including a fake stdio MCP server |

The routing matrix and static-serving policies live in
[`docs/architecture.md`](docs/architecture.md).

## Contributing

Please keep changes focused, preserve the dependency direction described in
[`AGENTS.md`](AGENTS.md), add or update tests for changed behavior, and run the
verification commands relevant to the change. Pull requests can use the
repository template at [`.github/pull_request_template.md`](.github/pull_request_template.md).

## License

NusaShell is released under the [MIT License](LICENSE).
