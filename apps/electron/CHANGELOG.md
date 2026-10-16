# Changelog

## [Unreleased]

## Earlier entries

### Added

- **Cross-platform Electron wrapper.** `apps/electron/` starts the matching
  native Go backend on loopback and loads the existing frontend in a secure
  BrowserWindow. Make targets cover development, wrapper tests, unpacked
  packaging, and native installer artifacts; GitHub Actions packages on
  Linux, Windows, and macOS runners.

### Fixed

- **Linux Electron development startup.** `make electron-dev` now detects an
  unavailable `chrome-sandbox` helper on user-owned filesystems and uses an
  explicit development-only fallback instead of aborting before the app opens.
