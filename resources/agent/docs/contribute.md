# Contribute to NusaShell

NusaShell contributions are made through the public GitHub repository:
<https://github.com/jahrulnr/NusaShell>. Use issues and pull requests there;
do not invent a private contribution channel.

## Set up a checkout

Prerequisites:

- Node.js 20 or newer
- pnpm 11 or newer
- Native build tools for `better-sqlite3`
  - Linux: Python 3, `make`, and a C++ compiler (for example,
    `sudo apt install python3 make g++`)
  - macOS: Xcode Command Line Tools (`xcode-select --install`)
  - Windows: Visual Studio Build Tools with the native C++ workload

Clone and install:

```bash
git clone https://github.com/jahrulnr/NusaShell.git nusashell
cd nusashell
pnpm install
```

Start the desktop development app with:

```bash
make dev
```

`pnpm desktop:dev` is the direct package-script alternative. Unpackaged
`--dev` runs keep durable state in `<repo>/.nusashell/` and use the dev runtime
port documented in [`settings.md`](settings.md).

## Before changing code

Read [`AGENTS.md`](../../../AGENTS.md) and the relevant architecture document. Keep
the broker-only boundary intact: plugin UI talks to the host bridge, and the
host talks to the backend over WebSocket. Keep the domain layer free of Electron,
filesystem, MCP SDK, and transport imports.

For repository plugin work, read [`build-plugin.md`](build-plugin.md) and use
`.agents/skills/build-nusashell-plugin/`. The in-app agent uses the seeded
`mcp-creator` skill and writes under userData/plugins, then registers the folder.
Headless MCP-only plugins and
windowed plugins have different user experiences; do not add a UI just to make a
headless integration appear on Home.

## Verify and submit

Run the focused tests for the package you changed, then the repository checks:

```bash
pnpm test:frontend
pnpm test:backend
```

Use the pull-request template at
[`.github/pull_request_template.md`](../../../.github/pull_request_template.md).
Describe the behavior changed, test commands, and any platform-specific notes.
If the change should ship, bump [`VERSION`](../../../VERSION) and add a matching
Keep a Changelog section in [`CHANGELOG.md`](../../../CHANGELOG.md).

Keep changes small and focused. Do not commit secrets, production credentials,
or generated build output. If you change a UI control or interaction, update the
UI knowledge map and regenerate its generated docs with `pnpm scan:ui-docs`.

Related: [`README.md`](../../../README.md), [`path-layout.md`](../../../docs/architecture/path-layout.md),
and [`build-plugin.md`](build-plugin.md).
