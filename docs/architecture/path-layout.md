# ADR: Cross-platform path layout

**Status:** Accepted
**Date:** 2026-08-01
**Supersedes:** ad-hoc `dataRoot` mixing in desktop bootstrap

## Context

NusaShell runs on Linux, macOS, and Windows. Several path bugs caused Windows
CI failures and risk data loss in packaged builds:

1. **MCP Roots `file://` URIs** were built via `` `file://${workspace}` ``
   string concatenation — on Windows this produces `file://C:\Users\...` which
   is not a valid file URI. The Files plugin parsed it back with
   `.replace(/^file:\/\//, "")`, losing the drive letter.
2. **Backend composer defaults** used `new URL(...).pathname` to resolve
   relative paths from `import.meta.url` — on Windows `.pathname` returns a
   leading-slash path without the drive letter (e.g. `/C:/Users/...`).
3. **Desktop `dataRoot`** when unpackaged pointed at the repo root, mixing
   durable state (docs-index cache) into the git checkout.
4. **Workspace label** used `ws.split("/").pop()` — breaks on Windows paths
   (`D:\proj` → no `/` separator).

## Decision

### Path placement policy

| Kind | Correct home | Must NOT use |
|---|---|---|
| Durable app state (prod) | Electron `app.getPath("userData")` under appData/nusashell | `os.tmpdir()`, repo root, random `/tmp` |
| Durable app state (dev only) | `<repo>/.nusashell/` via `app.setPath("userData", …)` | `os.tmpdir()`, repo root (outside `.nusashell/`), random `/tmp` |
| Bundled read-only assets | Package `resources/` or repo tree (via `getRuntimeRoot()`), including bundled `resources/plugins/` | userData, tmp |
| User-installed plugins | `<userData>/plugins/` (writable; agent `mcp_register` admission root) | repo `plugins/`, bundled `resources/plugins/`, arbitrary URLs/downloads |
| Ephemeral extract/scratch | `os.tmpdir()` + `path.join` + unique prefix `nusashell-*` | userData, config dirs |
| User workspace | Conversation-chosen absolute path (OS-native) | reinvented under tmp |

### Implementation rules

1. **`file://` URIs**: always use `pathToFileURL(path.resolve(p)).href` to
   build, `fileURLToPath(uri)` to parse. Never string-concatenate `file://`.
2. **`import.meta.url` path resolution**: use `fileURLToPath(new URL(rel,
   import.meta.url))` — never `.pathname`.
3. **Desktop `stateRoot`**: `getDataRoot()` always returns
   `app.getPath("userData")`. All durable state (settings, logs, skills,
   memories, conversations, DB, docs-index cache, mail settings) lives under
   it. Bundled assets (prompts, docs, plugins) use `getRuntimeRoot()`.
4. **Installer**: extract under `join(tmpdir(), "nusashell-plugin-*")`; final
   copy into `pluginsRoot` under stateRoot. Clean up extract dir on success
   and failure.
5. **Workspace label**: split on both separators `[\\/]/` to get basename.

### Dev/prod runtime isolation (exception to rule 3)

Unpackaged `--dev` mode may redirect `userData` to `<repo>/.nusashell/`
(gitignored) so concurrent prod + dev runs don't fight on settings, DB, or
conversations, and local state stays in-tree for easy tracing. This is the
**only** exception to "durable state never lives in the repo."

- **Mode SoT:** `app.isPackaged` (not `NODE_ENV`).
  `isDev = !app.isPackaged && argv.includes("--dev")`.
- **Port:** prod/non-dev defaults to `9130`; unpackaged `--dev` defaults to
  `9131`. `NUSASHELL_PORT` always wins when set. The main process exports the
  resolved port via `process.env.NUSASHELL_PORT` so preload and window-manager
  derive the same value from the shared `resolveWsPort` helper.
- **Dev-only hardening:** `--no-sandbox`, debug `logLevel`, Vite renderer URL,
  and plugin window DevTools are gated on `isDev`. Packaged builds never leak
  these even if `--dev` is appended.
- **AI stub:** `NUSASHELL_AI_STUB` is ignored when packaged.
- This exception **must never apply when `app.isPackaged`** — packaged
  userData always stays under appData/nusashell.

### OS examples for packaged userData

Electron resolves `app.getPath("userData")` below the OS application-data
location. The current desktop app uses the lowercase `nusashell` app-data name:

| OS | Typical packaged/non-dev path |
|---|---|
| Linux | `~/.config/nusashell/` |
| macOS | `~/Library/Application Support/nusashell/` |
| Windows | `%APPDATA%\\nusashell\\` |

These are examples for the default environment, not promises that every custom
Electron packaging or environment override has the same parent directory. The
agent-facing [`resources/agent/docs/data-locations.md`](../../resources/agent/docs/data-locations.md)
doc also inventories the files stored below this root and explains plugin
installation paths. Use a live `mcp_list.installPath` for a specific plugin.

## Consequences

- Packaged and unpackaged-without-`--dev` runs write durable state under
  Electron userData (e.g. `~/.config/nusashell/` on Linux). First run after
  the original path-fix change re-created settings/skills/memory there.
- Unpackaged `--dev` runs write durable state under `<repo>/.nusashell/`
  (gitignored) so dev and prod can run concurrently without sharing
  settings/DB/conversations, and dev state is easy to inspect in-tree.
- Backend composer defaults (`.nusashell/agent/...` relative to `import.meta.url`)
  remain as dev/backend-only fallbacks. Desktop always injects absolute
  `stateRoot` paths so packaged Electron never relies on those fallbacks.
- Windows paths round-trip correctly through MCP Roots: `C:\Users\...\proj` →
  `file:///C:/Users/.../proj` → `C:\Users\...\proj`.
