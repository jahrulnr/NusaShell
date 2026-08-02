# Settings

NusaShell settings control the runtime, AI provider, plugins path, and context limits.

## Environment variables

- `NUSASHELL_PORT`: HTTP/WebSocket port. Defaults to `9130` in prod/non-dev and
  `9131` in unpackaged `--dev` mode. Always wins over the mode default when set.
- `NUSASHELL_HOST`: bind address (default `0.0.0.0`).
- `NUSASHELL_PLUGINS_ROOT`: optional explicit plugin root for backend deployments. The desktop app defaults user installs to `{userData}/plugins/` and keeps bundled `resources/plugins/` separate.
- `NUSASHELL_DB_PATH`: SQLite path for persistent plugin metadata.
- `NUSASHELL_AI_PROVIDER`: name of the AI provider.
- `NUSASHELL_AI_BASE_URL`: base URL of an OpenAI-compatible API.
- `NUSASHELL_AI_API_KEY`: API key.
- `NUSASHELL_AI_MODEL`: model name.
- `NUSASHELL_AI_STUB`: set to `true` to use the static stub provider. Ignored
  in packaged builds (prod never uses the stub).
- `NUSASHELL_AI_STREAM`: set to `false` to disable streaming.
- `NUSASHELL_AI_VISION`: `on`, `off`, or `auto`.

## Runtime modes

The desktop shell uses `app.isPackaged` as the production signal (not
`NODE_ENV`). Unpackaged builds are only treated as dev when `--dev` is passed.

| Mode | WS port default | Durable state location |
| --- | --- | --- |
| Packaged (prod) | `9130` | Electron userData under appData/nusashell |
| Unpackaged without `--dev` | `9130` | Electron userData under appData/nusashell |
| Unpackaged with `--dev` | `9131` | `<repo>/.nusashell/` (gitignored, in-tree for tracing) |

The OS-specific examples and the file inventory are in
[`data-locations.md`](data-locations.md). Uninstall instructions are in
[`uninstall.md`](uninstall.md); they distinguish removing the app from wiping
its data.

Dev-only behavior (`--no-sandbox`, debug log level, Vite renderer URL, plugin
window DevTools) is gated on `isDev` and never leaks into packaged builds.

## Context limits

`NUSASHELL_AI_CONTEXT_MAX_INPUT_TOKENS`, `RESERVE_TOKENS`, `RECENT_TURNS`, and `SUMMARY_MAX_CHARS` tune compaction. Increase `MAX_INPUT_TOKENS` when the provider supports a larger window.

## Defaults

If no environment variables are set, the backend starts on `0.0.0.0:9130` (prod) or `0.0.0.0:9131` (dev) with an in-memory plugin repository and a stub AI provider (stub is forced off in packaged builds).
