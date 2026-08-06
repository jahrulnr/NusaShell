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
#
# Safety: only installs the app binary under ~/.local (or ~/Applications).
# Never writes durable app state under ~/.config/nusashell (or OS appData
# equivalents). verify:package-runtime refuses to ship plugin runtime state
# such as notes.json that local tests/dev may leave under plugins/.
#
# Package pre-cleans apps/desktop/out via rename-away so a running NusaShell
# (or fuseblk .fuse_hidden* tombstones) cannot break electron-forge with ENOTEMPTY.
install:
	pnpm --filter @nusashell/desktop run package
	pnpm --filter @nusashell/desktop run verify:package-runtime
	bash scripts/install-local.sh

# Lightweight gates that prove install/package safety contracts without a full
# electron-forge package (which is slow and requires a free out/ tree).
test-install-safety:
	pnpm --filter @nusashell/desktop exec vitest run tests/stage-plugins-resource.test.ts tests/clean-package-output.test.ts
	pnpm exec vitest run scripts/install.test.mjs
	pnpm --filter @nusashell/example-files exec vitest run tests/fs-service.test.js -t "greps a single file"