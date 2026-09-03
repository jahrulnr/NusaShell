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

1. whether to install the Electron desktop wrapper;
2. whether to install the desktop pet (Linux only);
3. whether to install first-party plugins from `NusaShell-mcp`.

The default answer is no for all optional components. Choices can be made
without a prompt, which is useful for automation:

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh \
  | bash -s -- --install-electron --install-pets --install-mcp
```

Equivalent environment overrides are `NUSASHELL_INSTALL_ELECTRON=1|0`,
`NUSASHELL_INSTALL_PETS=1|0`, and `NUSASHELL_INSTALL_MCP=1|0`. Set
`NUSASHELL_NON_INTERACTIVE=1` to skip all of them unless an explicit `1`
override or install flag is supplied.

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex
```

For explicit choices from a checked-out copy:

```powershell
& .\scripts\install.ps1 -InstallElectron -InstallMcp
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
the versioned Go layout for the core.

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

The default runtime expects the NusaShell Go core on `ws://127.0.0.1:9999/ws`
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
make electron-version
make electron-version-sync
make electron-version-check
make release-index-check
```

(`apps/pets/VERSION` is read via `node scripts/version.mjs read-pets`.)

Useful local packaging commands are:

```bash
make go-release              # package the Go core for this Unix platform
make go-release-manifest    # create release/go/latest.json
make electron-package       # package the wrapper only; no Go binary included
make electron-release-linux # create its standalone Linux tar payload
make electron-release-manifest
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
make go-dev                 # native Go server
make electron-dev           # stage a dev backend and run the wrapper
make electron-test          # wrapper policy/unit tests
make electron-ui-test       # real Electron renderer smoke flow
make electron-package       # wrapper-only unpacked package
make electron-install-local # install that local wrapper package
make electron-dist          # native Electron installer for this OS
```

`electron-dev` and `electron-ui-test` stage an ignored current-platform Go
binary under `apps/electron/runtime/` solely for local development/testing.
`electron-package` and `electron-dist` do not stage or package that binary.

On Linux, Electron first tries the unprivileged user-namespace sandbox. If
the host prevents it and `chrome-sandbox` is not `root:root` with mode `4755`,
the Electron launcher explicitly adds `--no-sandbox` and reports that choice
to stderr. This is a launcher fallback, not a Go core setting.

## Uninstall

Close NusaShell first. Remove the relevant program paths:

- Linux: `~/.local/share/nusashell`,
  `~/.local/share/nusashell-electron`, `~/.local/share/nusashell-pets`,
  `~/.local/bin/nusashell`, `~/.local/bin/nusashell-desktop`,
  `~/.local/bin/nusashell-pets`, and the desktop entry.
- macOS: `~/.local/share/nusashell`, `~/.local/bin/nusashell`, and
  `~/Applications/NusaShell Desktop.app`.
- Windows: `%LOCALAPPDATA%\Programs\NusaShell` and
  `%LOCALAPPDATA%\Programs\NusaShell-Electron`, plus shortcuts.

Keep the application-data directory unless a full data wipe is intended.
