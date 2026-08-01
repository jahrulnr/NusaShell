# Install NusaShell

Linux and macOS (no administrator access required):

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash
```

With wget: `wget -qO- https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash`.
On Windows PowerShell: `irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex`.

The installer downloads the release manifest and payload, verifies SHA-256 before extracting, and never writes outside your user profile. Set `NUSASHELL_VERSION=0.1.0` to pin a release. Releases are published automatically from `master` when `VERSION` is new.

On Linux, releases live in `~/.local/share/nusashell/versions/`; `current` points to the active version and `~/.local/bin/nusashell` launches it. A desktop-menu entry is written under `~/.local/share/applications`. Updates download a new version, verify it, and switch `current`, leaving the previous version intact. AppImage builds continue to use Electron's updater; system packages should be updated through the system package manager.

Electron needs unprivileged user namespaces for its sandbox. If your Linux system disables them, the installer adds `--no-sandbox` and prints a notice. Prefer enabling them with `sudo sysctl kernel.unprivileged_userns_clone=1` rather than relying on that fallback.

macOS installs to `~/Applications/NusaShell.app`; the installer removes the quarantine attribute when present. If `~/.local/bin` is not on Linux's PATH, add the exact line printed by the installer to your shell profile.

To uninstall the Linux user-space installation, remove `~/.local/share/nusashell`, `~/.local/bin/nusashell`, `~/.local/share/applications/nusashell.desktop`, and optionally `~/.config/autostart/nusashell.desktop`. Remove `~/.config/nusashell` too only for a full data wipe.

Before piping any installer to a shell, you can inspect it first: download it, read it, then run it. Checksums protect the release archive after download; a pinned version makes that inspection reproducible.
