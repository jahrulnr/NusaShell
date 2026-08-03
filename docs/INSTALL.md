# Install NusaShell

Linux and macOS (no administrator access required):

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash
```

With wget: 
```bash
wget -qO- https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash
```

On Windows PowerShell: 
```bash
irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex
```

The installer downloads the release manifest and payload, verifies SHA-256 before extracting, and never writes outside your user profile. Set `NUSASHELL_VERSION=0.1.0` to pin a release. Releases are published automatically from `master` when `VERSION` is new.

On Linux, releases live in `~/.local/share/nusashell/versions/`; `current` points to the active version and `~/.local/bin/nusashell` launches it. A desktop-menu entry is written under `~/.local/share/applications`. Updates download a new version, verify it, and switch `current`; the installer keeps only the newly installed version and exactly one previous version. For example, `0.1.6` → `0.1.7` keeps both, then `0.1.7` → `0.1.9` removes `0.1.6` and keeps `{0.1.7, 0.1.9}`. AppImage builds continue to use Electron's updater; system packages should be updated through the system package manager.

Electron ships a `chrome-sandbox` helper that must be owned by root with mode
`4755`. Without that, Chromium aborts even when user namespaces are enabled.
The installer detects this before claiming success: it prompts (via `/dev/tty`,
so `curl | bash` still works) to run a one-time `sudo chown`/`chmod`. If you
decline, sudo fails, or `NUSASHELL_NON_INTERACTIVE=1` is set, it renames the
helper and launches with `--no-sandbox` instead. To restore the real sandbox
later, fix the renamed helper and re-run the installer.

macOS installs to `~/Applications/NusaShell.app`; the installer removes the quarantine attribute when present. If `~/.local/bin` is not on Linux's PATH, add the exact line printed by the installer to your shell profile.

### GUI launch and Node / nvm tools

Desktop-menu launches inherit a stripped `PATH` (no interactive shell profile).
NusaShell merges the login-shell `PATH` at boot and, for absolute MCP/ACP
commands, prepends the command's directory so shebang scripts like nvm's `npx`
can find sibling `node`. If a native MCP still fails with `spawn npx ENOENT` or
`/usr/bin/env: 'node': No such file or directory`, either:

- launch NusaShell from a terminal that already loaded nvm/fnm, or
- set the plugin's Environment JSON `PATH` to include that Node `bin` directory, or
- point `command` at an absolute binary that does not rely on `env node`.

The Windows PowerShell installer stores versions under
`%LOCALAPPDATA%\\Programs\\NusaShell\\`, keeps the active `current` junction
there, and creates a Start Menu shortcut at
`%APPDATA%\\Microsoft\\Windows\\Start Menu\\Programs\\NusaShell.lnk`.

## Uninstall

Close NusaShell before removing its files. To uninstall the Linux user-space
installation, remove `~/.local/share/nusashell`, `~/.local/bin/nusashell`,
`~/.local/share/applications/nusashell.desktop`, and optionally
`~/.config/autostart/nusashell.desktop`. Remove `~/.config/nusashell` too only
for a full data wipe; it contains durable app state and is not required to
remove the program. Older releases may have used `~/.config/@nusashell/desktop/`;
that legacy path is obsolete and is not read or recreated by the app, so move or
delete it manually if no longer needed.

On macOS, remove `~/Applications/NusaShell.app` from Finder or the Trash. For a
full data wipe, also remove `~/Library/Application Support/nusashell/`.

On Windows, remove `%LOCALAPPDATA%\\Programs\\NusaShell\\` and the Start Menu
shortcut if the user-space install has no registered uninstaller. For a full
data wipe, also remove `%APPDATA%\\nusashell\\`. Removing the app and wiping
its data are separate choices on every OS.

Before piping any installer to a shell, you can inspect it first: download it, read it, then run it. Checksums protect the release archive after download; a pinned version makes that inspection reproducible.
