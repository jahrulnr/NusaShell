# Install NusaShell

NusaShell has three separate programs:

- `nusashell` is the Go core and always gets installed.
- `nusashell-desktop` is an optional Electron wrapper. It starts the already
  installed Go core on loopback and loads the same web application.
- `nusashell-pets` is an optional desktop pet overlay (Linux only). It renders
  the NusaShell mascot as an alpha-shaped, always-on-top SDL2 window and
  follows agent activity over the local WebSocket.

The release artifacts and manifests are separate as well. `latest.json` is
for the Go core; `electron-latest.json` is for the Electron wrapper;
`pets-latest.json` is for the desktop pet. Each stream has its own immutable
GitHub tag (`go-v<version>`, `electron-v<version>`, or `pets-v<version>`), so
either one can be released without rebuilding the other. Electron never
embeds or replaces the Go binary.

## Release installer

Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash
```

The installer always installs the Go core, then asks:

1. whether to install the login service for the Go core (autostart);
2. whether to install the Electron desktop wrapper;
3. whether to install the desktop pet (Linux only);
4. whether to install first-party plugins from `NusaShell-mcp`.

The default answer is no for all optional components. Choices can be made
without a prompt, which is useful for automation:

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh \
  | bash -s -- --install-service --install-electron --install-pets --install-mcp
```

Equivalent environment overrides are `NUSASHELL_INSTALL_SERVICE=1|0`,
`NUSASHELL_INSTALL_ELECTRON=1|0`, `NUSASHELL_INSTALL_PETS=1|0`, and
`NUSASHELL_INSTALL_MCP=1|0`. Set `NUSASHELL_NON_INTERACTIVE=1` to skip all
of them unless an explicit `1` override or install flag is supplied.

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex
```

For explicit choices from a checked-out copy:

```powershell
& .\scripts\install.ps1 -InstallService -InstallElectron -InstallMcp
```

Before piping a remote script, download and inspect it first. The installers
use HTTPS, verify SHA-256 from the product-specific release manifest, reject
unsafe archive names/paths, resolve each stream through
`release-versions.json`, and activate a completed version atomically.

Pin a release for a reproducible install:

```bash
NUSASHELL_VERSION=0.1.0 bash scripts/install.sh --no-electron --no-pets --no-mcp
```

Pin the three streams independently:

```bash
NUSASHELL_VERSION=1.2.3 NUSASHELL_ELECTRON_VERSION=2.0.0 NUSASHELL_PETS_VERSION=0.1.3 \
  bash scripts/install.sh --install-electron --install-pets --no-mcp
```

```powershell
& .\scripts\install.ps1 -Version 0.1.0 -NoElectron -NoMcp
```

PowerShell uses `-ElectronVersion` for the optional wrapper; the desktop pet
is Linux-only and is therefore never offered by the Windows installer. The
equivalent environment variables are `NUSASHELL_ELECTRON_VERSION` and
`NUSASHELL_PETS_VERSION`. When an optional component is selected without a
pin, the installer gets that stream's latest release even if the Go core has
a different version.

## Installation layout

The program files and NusaShell data are deliberately separate.

| Platform | Go core | Electron wrapper | Desktop pet | MCP/application data |
| --- | --- | --- | --- | --- |
| Linux | `~/.local/share/nusashell/versions/<version>/`, launcher `~/.local/bin/nusashell` | `~/.local/share/nusashell-electron/versions/<version>/`, launcher `~/.local/bin/nusashell-desktop` | `~/.local/share/nusashell-pets/versions/<version>/`, launcher `~/.local/bin/nusashell-pets` | `~/.config/nusashell/plugins/` |
| macOS | `~/.local/share/nusashell/versions/<version>/`, launcher `~/.local/bin/nusashell` | `~/Applications/NusaShell Desktop.app` | not available | `~/Library/Application Support/nusashell/plugins/` |
| Windows | `%LOCALAPPDATA%\Programs\NusaShell\versions\`, launcher `nusashell.cmd` | `%LOCALAPPDATA%\Programs\NusaShell-Electron\versions\`, Start Menu/Desktop shortcuts | not available | `%APPDATA%\nusashell\plugins\` |

Linux and Windows keep a `current` symlink/junction and retain the active
release plus one previous release. The old version is not removed while its
process is still running. macOS uses an application bundle for Electron and
the versioned Go layout for the core. Windows also writes a `NusaShell.lnk`
Start Menu shortcut to the `nusashell.cmd` launcher.

## Login service (autostart)

The optional login service supervises the Go core server under the current
user account: it starts at login and restarts the server if it exits. Run
the installer with `--install-service` (or answer yes at the prompt), or
manage it directly afterwards:

```bash
nusashell service install     # create + enable + start
nusashell service status      # installed/loaded/running + drift check
nusashell service stop
nusashell service start
nusashell service restart
nusashell service uninstall   # disable + remove the definition
```

Each platform uses its native user-level mechanism — no root, no system
scope:

| Platform | Mechanism | Definition | Service logs |
| --- | --- | --- | --- |
| Linux | systemd user unit | `~/.config/systemd/user/nusashell.service` | journal (`journalctl --user -u nusashell`) |
| macOS | LaunchAgent | `~/Library/LaunchAgents/id.nusashell.core.plist` | `<data>/logs/service-stdout.log`, `service-stderr.log` |
| Windows | Scheduled Task `NusaShell Core` | task XML + `<data>/service/nusashell-service.cmd` (+ hidden `.vbs` launcher) | `<data>\logs\service.log` |

Behavior shared across platforms:

- The definition bakes in the current install (`current` symlink/junction
  path) and `NUSASHELL_DATA_DIR`; `NUSASHELL_HOST`, `NUSASHELL_PORT`, and
  `NUSASHELL_ALLOW_REMOTE` are inherited only when set at install time.
- Restart is automatic (`Restart=always`, launchd `KeepAlive`, Windows
  restart-on-failure), and the definition always sets `NUSASHELL_SERVICE=1`
  so the supervised process can identify itself. Mutating `nusashell
  service` commands refuse to run from inside that process, preventing
  agent-initiated stop/restart loops.
- Re-running `nusashell service install` regenerates the definition and
  backs the previous one up as `.bak`; rewriting a symlinked definition is
  refused. `nusashell service status` flags drift between the installed
  definition and the current install.
- On Linux, when the systemd user bus is unreachable (fresh SSH session),
  the installer attempts `loginctl enable-linger` and otherwise prints the
  remediation. On Windows, hosts that deny Scheduled Task creation fall
  back to a Startup-folder entry running the same hidden launcher.

`nusashell service uninstall` removes the definition (the macOS plist is
moved to the Trash first) and the Windows startup fallback entry.

The installer never deletes conversations, credentials, provider settings,
skills, memory, or plugins. Remove the program paths only for an uninstall;
remove the data directory separately when a full wipe is intended.

## Desktop pet (Linux)

The desktop pet is an opt-in component like Electron, but Linux-only for now:
the Unix installer offers it only on Linux and the Windows installer does not
install it at all. The payload contains the `nusashell-pets` binary and its
`assets/` folder (the hatch-pet v2 WebP atlas and `config.json`); the
launcher always passes `--assets <current>/assets/pets`, so the pet finds its
artwork regardless of the current working directory. Run it with:

```bash
nusashell-pets
```

The default runtime expects the NusaShell Go core on `ws://127.0.0.1:10994/ws`
and an X11 (or XWayland) session. Native Wayland is rejected with a hint,
because the always-on-top and shaped-input behavior needs X11 Shape.

## NusaShell-mcp

MCP installation is explicit opt-in. The installer reads the catalog from
`NusaShell-mcp/versions.json` and installs the selected plugin folders below
the Go app data directory. By default the catalog keys are `kanban`, `notes`,
`whatsapp`, and `telegram`; limit them with `NUSASHELL_MCP_PLUGINS`, for
example `NUSASHELL_MCP_PLUGINS=notes,kanban`.

On Linux it first uses the matching plugin release asset. If a native asset is
not available, and on macOS/Windows where the current upstream releases may
not contain native binaries, it downloads the tagged source and builds the
stdio server locally. That fallback requires Go. A selected MCP install
failure is reported as an installer failure; it is never silently reported as
complete.

Set `NUSASHELL_DATA_DIR` when the core uses a custom data directory. The
installer honors it when placing plugins.

## Versioning and releases

The streams have independent Semantic Version sources:

- `VERSION` is the Go core version.
- `apps/electron/VERSION` is the Electron wrapper version.
- `apps/pets/VERSION` is the desktop pet version (Linux only).

Read, synchronize, and check them with:

```bash
make go-version
make -C apps/electron version
make -C apps/electron version-sync
make -C apps/electron version-check
make release-index-check
```

(`apps/pets/VERSION` is read via `node scripts/version.mjs read-pets`.)

Useful local packaging commands are:

```bash
make go-release                    # package the Go core for this Unix platform
make go-release-manifest           # create release/go/latest.json
make -C apps/electron package      # package the wrapper only; no Go binary included
make -C apps/electron release-linux
make -C apps/electron release-manifest
```

On a push to `master`, GitHub Actions first detects which product paths
changed, following the release-on-changes pattern used by NusaShell-mcp.
Go changes build and publish only the Go matrix; `apps/electron/**` changes
build and publish only the Electron matrix; `apps/pets/**` changes build and
publish only the Linux pets matrix; shared icon changes run both Go and
Electron. The release jobs also compare each VERSION value with its
corresponding `release-versions.json` pointer. A stream whose version is
ahead of its pointer is retried even when the follow-up commit only fixes CI
or tests. The Go, Electron, and pets gates are independent: a failed gate for
one stream does not block the others. Each successful publisher updates only
its own pointer, preserving the stream that is still pending. CI checks the
corresponding immutable tag before publishing. If the tag already exists,
that stream's publisher is skipped without failing the workflow and its
release pointer remains unchanged. Bump the corresponding VERSION file before
the next product release; documentation, CI, and release-tooling-only changes
do not require a product bump.

The index is committed by GitHub Actions with `[skip ci]`. It is a small,
tracked pointer document, not a copy of release binaries, and lets installers
find the latest Go and Electron releases even when their versions differ.

## Development from a checkout

```bash
make go-dev                         # native Go server
make -C apps/electron dev           # stage a dev backend and run the wrapper
make -C apps/electron test          # wrapper policy/unit tests
make -C apps/electron ui-test       # real Electron renderer smoke flow
make -C apps/electron package       # wrapper-only unpacked package
make -C apps/electron install-local # install that local wrapper package
make -C apps/electron dist          # native Electron installer for this OS
```

`make -C apps/electron dev` and `ui-test` stage an ignored current-platform Go
binary under `apps/electron/runtime/` solely for local development/testing.
`package` and `dist` do not stage or package that binary.

On Linux, Electron first tries the unprivileged user-namespace sandbox. If
the host prevents it and `chrome-sandbox` is not `root:root` with mode `4755`,
the Electron launcher explicitly adds `--no-sandbox` and reports that choice
to stderr. This is a launcher fallback, not a Go core setting.

## Uninstall

Stop the service first when it is installed: `nusashell service uninstall`
(Linux: removes `~/.config/systemd/user/nusashell.service`; macOS: moves
`~/Library/LaunchAgents/id.nusashell.core.plist` to the Trash; Windows:
deletes the Scheduled Task and the Startup fallback entry). Close NusaShell
and remove the relevant program paths:

- Linux: `~/.local/share/nusashell`,
  `~/.local/share/nusashell-electron`, `~/.local/share/nusashell-pets`,
  `~/.local/bin/nusashell`, `~/.local/bin/nusashell-desktop`,
  `~/.local/bin/nusashell-pets`, and the desktop entries
  (`nusashell-desktop.desktop`, `nusashell-pets.desktop`).
- macOS: `~/.local/share/nusashell`, `~/.local/bin/nusashell`, and
  `~/Applications/NusaShell Desktop.app`.
- Windows: `%LOCALAPPDATA%\Programs\NusaShell` and
  `%LOCALAPPDATA%\Programs\NusaShell-Electron`, plus the Start Menu and
  desktop shortcuts.

Keep the application-data directory unless a full data wipe is intended.
