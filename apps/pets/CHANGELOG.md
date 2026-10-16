# Changelog

## [Unreleased]

## Earlier entries

### Changed

- **Desktop pet no longer writes a `.desktop` menu entry.** Pets install
  still places `~/.local/bin/nusashell-pets` and is launched from the
  NusaShell UI; it does not create `nusashell-pets.desktop`.

### Fixed

- **Desktop pet Electron launch.** The pets stream (`0.2.3`) adds
  `--no-sandbox` when an older launcher shim is paired with a payload marked
  `chrome-sandbox.disabled`.

- **Desktop pet click opens Electron on hosts without a Chromium sandbox.**
  Pet spawn now prefers `~/.local/bin/nusashell-desktop` (installer shim with
  `--no-sandbox` when needed) and, when only the versioned binary is present
  beside `chrome-sandbox.disabled`, passes `--no-sandbox` itself. Spawning the
  raw `current/nusashell-desktop` previously died with "No usable sandbox".

- **Desktop pet auto-start survives the login DISPLAY race.** Go Launch
  merges `DISPLAY`/`XAUTHORITY` from the systemd user manager when missing,
  treats an immediate child exit as a failed spawn, and AutoLaunch retries
  until the overlay stays up (still a Go subprocess — no pets systemd unit).

- **Desktop pet icon is a single-instance toggle.** `settings.pets_launch`
  spawns one subprocess when the overlay is idle and stops that process
  when it is already running, so spam-clicking the sidebar icon cannot
  open multiple pets.

- **Desktop pet connects to the live Go listen address.** Spawn now passes
  `NUSASHELL_HOST`, `NUSASHELL_PORT`, `NUSASHELL_WS_URL`, and `--ws-url`
  from the running process, so a baked-in `config.json` `ws_url` (including
  an older install still pointing at port 9999) no longer wins.

- **Pets install/launch tests no longer assume Linux.** Five fake-installer
  tests gated `Supported` on `runtime.GOOS == "linux"`, so on macOS and
  Windows the install-start handler refused and the tests panicked/failed.
  The single-flight, error-bus, and launch tests now use
  `Supported: true` like the other success-path tests.
