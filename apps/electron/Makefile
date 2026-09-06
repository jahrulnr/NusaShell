# NusaShell Electron wrapper developer tooling.
#
# Run from this directory (`make test`) or from the repository root:
#   make -C apps/electron test

REPO_ROOT := $(abspath ../..)
VERSION_FILE ?= VERSION
ELECTRON_VERSION := $(shell tr -d '\r\n' < "$(VERSION_FILE)")
GO_VERSION := $(shell tr -d '\r\n' < "$(REPO_ROOT)/VERSION")
GO_LDFLAGS ?= -X main.version=$(GO_VERSION)
VERSION_SCRIPT := $(REPO_ROOT)/scripts/version.mjs

.PHONY: deps version version-sync version-check test ui-test build-backend dev package install-local dist release-linux release-manifest

## version: print the Electron wrapper release version.
version:
	@node "$(VERSION_SCRIPT)" read

## version-sync: update package.json and package-lock.json from VERSION.
version-sync:
	@node "$(VERSION_SCRIPT)" sync

## version-check: fail when package metadata differs from VERSION.
version-check:
	@node "$(VERSION_SCRIPT)" check

## deps: install the pinned Electron wrapper dependencies.
deps: version-check
	npm ci

## test: run Electron wrapper unit tests without starting a GUI.
test: deps
	npm test

## ui-test: launch the real Electron renderer and exercise web UI flows.
ui-test: build-backend deps
	npm run test:ui

## build-backend: stage the current-platform Go backend for local Electron
## development and renderer tests. The staged binary is ignored and is never
## copied into a release package.
build-backend:
	mkdir -p runtime
	cd "$(REPO_ROOT)" && go build -buildvcs=false -ldflags "$(GO_LDFLAGS)" -o "$(CURDIR)/runtime/nusashell" ./cmd/nusashell

## dev: run the web UI inside Electron with disk-backed frontend assets.
dev: build-backend deps
	NUSASHELL_DEV=1 npm run dev

## package: create an unpacked Electron wrapper directory.
## The Go backend is intentionally not embedded; dev/UI targets stage it only
## for the local process they launch.
package: deps
	npm run package:dir

## install-local: package and install Electron under the user profile.
install-local: package
	@case "$$(uname -s)" in \
		MINGW*|MSYS*|CYGWIN*) powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$(REPO_ROOT)/scripts/install-local.ps1";; \
		*) bash "$(REPO_ROOT)/scripts/install-local.sh";; \
	esac

## dist: create native Electron wrapper artifacts for this platform.
dist: deps
	npm run dist

## release-linux: create the standalone Electron payload used by the
## optional Electron part of install.sh on Linux.
release-linux: package
	@case "$$(uname -s)" in \
		Linux) ;; \
		*) echo "release-linux must run on Linux" >&2; exit 1;; \
	esac
	@version="$(ELECTRON_VERSION)"; \
	mkdir -p dist/release; \
	tar -C dist/linux-unpacked -czf "$$(pwd)/dist/release/nusashell-electron-$${version}-linux-x64.tar.gz" .; \
	sha256sum "dist/release/nusashell-electron-$${version}-linux-x64.tar.gz" > "dist/release/nusashell-electron-$${version}-linux-x64.tar.gz.sha256"

## release-manifest: index locally produced Electron payloads.
release-manifest: version-check
	node "$(REPO_ROOT)/scripts/release-manifest.mjs" "$(ELECTRON_VERSION)" dist/release dist/release/electron-latest.json electron
