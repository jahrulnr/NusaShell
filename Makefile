dev:
	@echo "==> Clearing Vite + build caches to avoid stale-module false positives"
	rm -rf apps/desktop/.vite apps/desktop/node_modules/.vite node_modules/.vite node_modules/.cache
	@echo "==> Building plugin-sdk (renderer imports from source via Vite)"
	pnpm --filter @nusashell/plugin-sdk run build
	pnpm --filter @nusashell/example-mail run build
	pnpm --filter @nusashell/desktop run dev

test:
	pnpm -r test

# Package the desktop app and install it from the local repo into the user's
# home directory (~/.local/share/nusashell on Linux, ~/Applications on macOS).
# Uses `electron-forge package` (not `make`) so we skip AppImage/deb building —
# those distributables are only needed for GitHub releases, not local installs.
# This mirrors scripts/install.sh (the curl installer) but sources the app
# from the local electron-forge package output instead of a GitHub release.
install:
	pnpm --filter @nusashell/desktop run package
	bash scripts/install-local.sh