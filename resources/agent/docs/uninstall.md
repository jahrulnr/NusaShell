# Uninstall NusaShell

First distinguish the action:

- **Quit** closes or hides the running app; it does not remove anything.
- **Uninstall the app** removes the NusaShell program and launcher entry.
- **Uninstall a plugin** removes one plugin from NusaShell but leaves the shell
  installed. Use the Plugins view's **Uninstall** action, or ask the agent to
  uninstall that plugin. Do not delete a plugin folder while its MCP process is
  running.
- **Wipe app data** is optional and removes conversations, settings, logs,
  memories, skills, and other durable state. Confirm this separately because it
  is not required to remove the program and cannot be undone by NusaShell.

## Remove the application

Close NusaShell first. Then use the instructions for the installation method.

### Linux user-space installation

The shell installer places the release tree under
`~/.local/share/nusashell/`, the launcher at `~/.local/bin/nusashell`, and the
desktop entry at `~/.local/share/applications/nusashell.desktop`. Remove those
paths:

```bash
rm -rf ~/.local/share/nusashell
rm -f ~/.local/bin/nusashell
rm -f ~/.local/share/applications/nusashell.desktop
```

If you enabled launch-at-login, also remove:

```bash
rm -f ~/.config/autostart/nusashell.desktop
```

For a full data wipe, remove `~/.config/nusashell/` only after confirming that
you no longer need the conversations, settings, logs, skills, or memories there.

### macOS user-space installation

The installer places the app at `~/Applications/NusaShell.app`. Move that app to
the Trash or remove it from Finder. If you created a Dock shortcut, remove the
shortcut separately.

For a full data wipe, remove:

```text
~/Library/Application Support/nusashell/
```

### Windows user-space installation

The PowerShell installer stores versions under
`%LOCALAPPDATA%\Programs\NusaShell\`, keeps the active `current` junction there,
and creates a Start Menu shortcut at
`%APPDATA%\Microsoft\Windows\Start Menu\Programs\NusaShell.lnk`.

Use Windows Settings → Apps to remove NusaShell when it is registered there. If
the user-space installation has no registered uninstaller, close the app and
remove the installation folder and shortcut from File Explorer or PowerShell:

```powershell
Remove-Item -Recurse -Force "$env:LOCALAPPDATA\Programs\NusaShell"
Remove-Item -Force "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\NusaShell.lnk"
```

For a full data wipe, remove:

```text
%APPDATA%\nusashell\
```

## Remove one plugin instead

For an agent-authored/user plugin, use `mcp_unregister` after confirmation; it
only removes a matching folder under `{userData}/plugins/`. Bundled built-ins
cannot be unregistered by the agent. Humans may use the Plugins view's
**Uninstall** action.

Open the Plugins view, select the plugin, stop it if it is running, and choose
**Uninstall**. This removes that plugin from the active `pluginsRoot`; it does
not remove NusaShell conversations or settings. For an exact installed location,
use `mcp_list` and read the plugin's `installPath` rather than guessing.

If the plugin is headless, it will not appear on the Home launcher grid. Manage
it from Plugins or with the `mcp_*` tools.

Related howtos: [`data-locations.md`](data-locations.md),
[`plugins.md`](plugins.md), and [`settings.md`](settings.md).
