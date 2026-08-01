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
| Durable app state | Electron `app.getPath("userData")` (always, packaged and unpackaged) | `os.tmpdir()`, repo root, random `/tmp` |
| Bundled read-only assets | Package `resources/` or repo tree (via `getRuntimeRoot()`) | userData, tmp |
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

## Consequences

- Unpackaged dev mode now writes durable state under Electron userData (e.g.
  `~/.config/NusaShell/` on Linux) instead of the repo root. First run after
  this change will re-create settings/skills/memory there.
- Backend composer defaults (`.nusashell/agent/...` relative to `import.meta.url`)
  remain as dev/backend-only fallbacks. Desktop always injects absolute
  `stateRoot` paths so packaged Electron never relies on those fallbacks.
- Windows paths round-trip correctly through MCP Roots: `C:\Users\...\proj` →
  `file:///C:/Users/.../proj` → `C:\Users\...\proj`.
