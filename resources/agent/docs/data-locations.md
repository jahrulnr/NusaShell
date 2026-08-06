# Where NusaShell stores data

NusaShell keeps durable application state separate from bundled read-only assets.
The exact path depends on the operating system and whether the app is a packaged
release or an unpackaged development run.

## Durable application state

| Runtime | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Packaged release or unpackaged run without `--dev` | `~/.config/nusashell/` | `~/Library/Application Support/nusashell/` | `%APPDATA%\\nusashell\\` |
| Unpackaged run with `--dev` | `<repo>/.nusashell/` | `<repo>/.nusashell/` | `<repo>\\.nusashell\\` |

The `--dev` row is an intentional exception for local development. It keeps a
dev run isolated from a packaged run and makes local state easy to inspect. The
folder is gitignored. A packaged build always uses the explicit `appData/nusashell`
location on every platform, even if someone passes `--dev` to the executable.
Older releases may have left `~/.config/@nusashell/desktop/` on Linux; that path
is obsolete and is not migrated or recreated.

If you are answering for a specific conversation, use its `runtime_os` value
when choosing a row. Do not assume Linux. The app may also use
`NUSASHELL_DB_PATH` to override the SQLite plugin metadata path.

## Files and folders

Within the durable state root, the desktop app may create:

| Path | Purpose |
| --- | --- |
| `agent-conversations.json` | Agent conversation history and checkpoints |
| `acp-providers.json` | ACP provider configuration |
| `ai-settings.json` | AI provider, model, and related settings |
| `user-prompt.md` | User-supplied prompt additions |
| `app-behavior.json` | Startup, close-to-tray, and related app behavior |
| `mail-settings.json` | Mail plugin settings |
| `logs/nusashell.log` | Main application log |
| `agent/docs-index/` | Search index for the bundled agent documentation |
| `skills/` | User-managed local skills |
| `memories/` | Agent memory and user-profile data |
| `plugins/` | Writable user-installed and in-app-agent-authored plugin folders |
| `plugins-data/` | Plugin-owned durable data (e.g. Notes `nusashell.notes/notes.json`, Kanban DBs) |
| `agent/jobs/` | Scheduled jobs and pipeline definitions / run history |

Plugin metadata is stored in SQLite when `NUSASHELL_DB_PATH` is configured by
the desktop composition root. `NUSASHELL_DB_PATH` is an explicit override, so
do not infer its filename from the OS path alone.

Notes are **not** stored inside the bundled/install plugin tree
(`resources/plugins/notes/`). They live under
`<userData>/plugins-data/nusashell.notes/notes.json` so `make install` and
release upgrades cannot overwrite production notes with local-dev or test
state left under the repository's `plugins/` tree.

## Bundled and installed plugins

User-installed and in-app-agent-authored plugin folders live under
`<userData>/plugins/`. This is separate from the bundled read-only
`resources/plugins/` tree. The in-app agent must use `mcp_register` after writing
an already-valid folder; do not silently treat a folder on disk as installed.

Packaged read-only plugin assets are shipped with the application, not copied to
userData. Packaging excludes plugin runtime state (`notes.json`) and first-party
`tests/` trees so local test data never rides into an install. For the Linux
user-space installer, the bundled plugin tree is below:

```text
~/.local/share/nusashell/versions/<version>/resources/plugins/
```

The macOS app bundle and Windows installed app have the equivalent
`resources/plugins/` directory inside their installation. An unpackaged
repository run uses:

```text
<repo>/plugins/
```

A human Add Plugin install and an agent `mcp_register` admission use the
writable user plugin root in the desktop app. For a particular plugin, ask
`mcp_list` and use its `installPath`; that live value is more reliable than
guessing an OS-specific location. `NUSASHELL_PLUGINS_ROOT` can explicitly choose
a backend plugin root.

## Avoid misleading answers

- Quitting NusaShell does not delete conversations, settings, plugins, or logs.
- Removing the app does not necessarily remove its user-data directory.
- Do not tell a user to delete a guessed database filename; first identify the
  configured path or explain that a full data wipe removes the whole state root.
- A conversation workspace is separate from NusaShell's application-data root.

Related howtos: [`uninstall.md`](uninstall.md), [`settings.md`](settings.md),
and [`getting-started.md`](getting-started.md).
