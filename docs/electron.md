# Electron desktop wrapper

The Electron application lives under `apps/electron/`. It is a deliberately
thin, cross-platform wrapper around the Go core and the existing web frontend:

1. Electron resolves an externally installed `nusashell` executable.
2. It starts that process on an ephemeral `127.0.0.1` port.
3. It waits for the HTTP root to become ready.
4. A secure `BrowserWindow` loads that same web application.
5. Electron stops the child process when the desktop app exits.

The packaged wrapper does not contain `resources/runtime/nusashell`. The Go
core is a separate release and must be installed first. The packaged runtime
looks in the user-local core layout and `PATH`; `NUSASHELL_ELECTRON_BACKEND`
can point to an explicit absolute binary for custom installations.

## One web implementation

The renderer is not a second application. The workspace picker, composer,
attachments, folder drops, WebSocket/SSE lifecycle, RPC calls, plugin windows,
and all other user interactions remain the native browser frontend in
`frontend/`. The Electron smoke test launches the real Go server and checks
the connection, composer input, file bridge, folder attachment branch, and
workspace picker RPC.

The browser/PWA mini-window control is currently hidden in Electron because
the renderer does not support that flow yet. It remains available in ordinary
browser mode while the Electron implementation is under review.

The app menu is disabled with `Menu.setApplicationMenu(null)` and the window
uses `autoHideMenuBar`, so Electron's default `File`/`Edit` bar is not shown.
This only removes the application menu; normal OS window controls remain
available.

## Local commands

```bash
make electron-install          # install pinned wrapper dependencies
make electron-test             # policy/helpers and packaging contract tests
make electron-ui-test          # real renderer interaction smoke test
make electron-dev              # stage a Go binary and launch Electron
make electron-package          # wrapper-only unpacked package
make electron-install-local    # install that local wrapper package
make electron-dist             # native wrapper installer for this OS
make electron-release-linux    # standalone Linux wrapper tar payload
make electron-release-manifest # electron-latest.json for local payloads
```

`electron-dev` enables `NUSASHELL_DEV=1`, so frontend edits are served from
disk. The ignored binary under `apps/electron/runtime/` is only for the dev
and UI-test process. `electron-package` and `electron-dist` do not depend on
`electron-build-backend` and do not include that file.

For an already-running loopback server:

```bash
NUSASHELL_ELECTRON_URL=http://127.0.0.1:9999/ npm --prefix apps/electron run dev
```

For a custom core binary:

```bash
NUSASHELL_ELECTRON_BACKEND=/absolute/path/to/nusashell \
  npm --prefix apps/electron run start
```

Both overrides are restricted to loopback web URLs; the wrapper does not load
arbitrary remote content.

## Security boundary

The packaged window uses context isolation, sandboxing, disabled Node
integration, and web security. The preload exposes only
`window.nusashellDesktop.getPathForFile(file)`, which the folder-attachment
flow needs. It does not expose filesystem, shell, or general IPC primitives to
the renderer. Same-origin plugin windows are allowed; external HTTP(S) links
go to the system browser.

## CI and release streams

GitHub Actions tests the wrapper independently and packages it on native
Linux, Windows, and macOS runners. A separate Go matrix builds the core. The
two streams use independent version files and immutable tags, while keeping
different artifact names and manifests:

- Go version: `VERSION`, tag `go-v<VERSION>`.
- Electron version: `apps/electron/VERSION`, tag
  `electron-v<VERSION>`.

- Go core: `latest.json`, with `nusashell-...` payloads.
- Electron wrapper: `electron-latest.json`, with
  `nusashell-electron-...`/`NusaShell-Electron-...` payloads.

The workflow detects changed paths before packaging. A Go-only change does
not rebuild Electron; an Electron-only change does not rebuild Go. The tracked
`release-versions.json` file points installers at the current release tag for
each stream, so the optional wrapper can be newer or older than the core.
Electron packaging never cross-embeds a backend from another operating system.

Linux/macOS release installation is in `scripts/install.sh`; Windows is in
`scripts/install.ps1`. Checkout-only wrapper installation is in
`scripts/install-local.sh` and `scripts/install-local.ps1`.
