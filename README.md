# NusaShell

[![CI](https://github.com/jahrulnr/NusaShell/actions/workflows/ci.yml/badge.svg)](https://github.com/jahrulnr/NusaShell/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](.github/pull_request_template.md)

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
  OpenAI-compatible services, DeepSeek, LM Studio, vLLM, and similar
  endpoints. API keys are kept in a local SQLite credential store.
- **MCP, skills, memory, and docs** — stdio MCP tools are available to the
  agent as `mcp__<server>__<tool>`; reusable Markdown skills, durable memory,
  and local documentation search round out the workspace.
- **Local-first delivery** — Go serves the frontend assets embedded with
  `embed.FS`. Production use needs no Node runtime, `node_modules`, or frontend
  build step.
- **Desktop pet (Linux)** — an optional SDL2 overlay (`nusashell-pets`) that
  renders the alpha-shaped NusaShell mascot always-on-top and reacts to agent
  activity over WebSocket. Built under `apps/pets` and installed as an
  opt-in release component, currently Linux-only.

## Quick start

Requirements:

- Go 1.26.5 or newer
- Node.js 24 or newer for frontend tests and E2E checks
- A desktop folder-dialog provider on Linux (install `zenity` if no compatible
  provider is already available). Windows and macOS use their system dialogs.

```bash
# SSH (recommended for contributors with commit access)
git clone git@github.com:jahrulnr/NusaShell.git
# or HTTPS
git clone https://github.com/jahrulnr/NusaShell.git
cd NusaShell

# Keep the local UI private to this machine.
export NUSASHELL_HOST=127.0.0.1
make run
```

Open `http://127.0.0.1:9999`, configure a provider, then choose a folder from
the composer’s workspace button. The selected folder is the workspace for the
active conversation.

### Release installer

The release installer installs the Go core first and then asks whether the
optional Electron desktop wrapper, the desktop pet (Linux only), and
`NusaShell-mcp` plugins should be installed:

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash
```

Use `--install-electron`/`--no-electron`,
`--install-pets`/`--no-pets`, and
`--install-mcp`/`--no-mcp` to choose explicitly. The desktop pet is currently
Linux-only; the installer on macOS ignores pets options. Set
`NUSASHELL_NON_INTERACTIVE=1` for unattended installs; optional components
default to not installed. See [`docs/INSTALL.md`](docs/INSTALL.md) for
Windows, layouts, version pinning, and MCP details.

Windows uses the equivalent PowerShell installer (pets is not offered there
yet):

```powershell
irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex
```

### Electron desktop app

The cross-platform Electron app is a thin wrapper around the same Go server
and native frontend. It does not duplicate the web UI or its interactions, and
its release package never embeds the Go binary:

```bash
make electron-dev       # build the local backend and launch Electron
make electron-test      # run wrapper tests without a GUI
make electron-ui-test   # launch Electron and test composer/workspace flows
make electron-package   # create an unpacked app directory
make electron-dist      # create a native installer for this OS
make electron-install-local # install the unpacked app into the user profile
make go-release         # package the standalone Go core for this Unix platform
```

See [`docs/electron.md`](docs/electron.md) for the runtime boundary,
development overrides, packaging, and CI details. See
[`docs/INSTALL.md`](docs/INSTALL.md) for release installers and versioning.

To run the Go app from anywhere as a `nusashell` command (the desktop release
uses the separate `nusashell-desktop` launcher):

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
make verify-local        # full local gate + Windows/macOS compile checks
make electron-test       # Electron wrapper unit tests
make electron-ui-test    # real Electron renderer interaction smoke test
make electron-dev        # run NusaShell in the Electron desktop shell
make electron-installer-test # installer/version/manifest contract tests
make go-release          # standalone Go core payload
make go-release-manifest # standalone Go release manifest
make go-version          # print the Go release version
make electron-version    # print the Electron release version
make release-index-check # validate independent release pointers
```

The local gate also runs the frontend test suite. Install its development
dependency once with `npm ci`. To run the same gate automatically before every
push, enable the repository hook once per clone:

```bash
make hooks
```

`make verify-local` runs native tests (with `-race` when the local C compiler is
available), formatting, UI documentation drift checks, vet, builds, frontend
tests, and compile-only checks for the Windows and macOS CI targets. A
cross-compiled test binary cannot run on a different kernel, so runtime
behavior remains covered by the native runners in GitHub Actions.

`make hooks` changes this clone’s repository-local `core.hooksPath`. If you
already use global Git hooks, preserve or chain them before enabling it.

The production frontend is plain browser JavaScript, but its browser-facing
checks use JSDOM:

```bash
npm ci
node --test frontend/tests/*.test.mjs
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
| `transport/` | HTTP RPC, WebSocket, and static asset serving |
| `cmd/nusashell/` | Composition root, configuration, lifecycle, and entrypoint |
| `frontend/` | Embedded native JavaScript, CSS, HTML, and frontend tests |
| `apps/electron/` | Cross-platform Electron wrapper, preload, packaging, and wrapper tests |
| `testdata/` | Stable fixtures, including a fake stdio MCP server |

The routing matrix and static-serving policies live in
[`docs/architecture.md`](docs/architecture.md).

## Contributing

Please keep changes focused, preserve the dependency direction described in
[`AGENTS.md`](AGENTS.md), add or update tests for changed behavior, and run the
verification commands relevant to the change. Pull requests can use the
repository template at [`.github/pull_request_template.md`](.github/pull_request_template.md).

## Recent changes

Notable additions, removals, and breaking changes live in
[`CHANGELOG.md`](CHANGELOG.md). The current `[Unreleased]` section covers the
PWA-grade shell, deterministic hydration placement, the dispatcher-family
tool refactor, and the experience-learning system (JSONL growth stores,
signal-triggered jobs, always-injected profile documents).

## License

NusaShell is released under the [MIT License](LICENSE).
