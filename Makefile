# NusaShell (Go) developer tooling.
#
# Gates follow the repository verification baseline:
# gofmt, go test, go test -race, go vet, go build.

VERSION_FILE ?= VERSION
NUSASHELL_VERSION := $(shell tr -d '\r\n' < "$(VERSION_FILE)")
ELECTRON_VERSION_FILE ?= apps/electron/VERSION
ELECTRON_VERSION := $(shell tr -d '\r\n' < "$(ELECTRON_VERSION_FILE)")
GO_LDFLAGS ?= -X main.version=$(NUSASHELL_VERSION)

.PHONY: all build test race vet fmt check verify-local hooks run go-dev install install-release test-frontend test-frontend-e2e scan-ui-docs scan-ui-docs-check gen-catalog gen-catalog-check go-version electron-version electron-version-sync electron-version-check electron-install electron-test electron-installer-test electron-ui-test electron-build-backend electron-dev electron-package electron-install-local electron-dist electron-release-linux electron-release-manifest go-release go-release-manifest release-index-check

all: check

## build: compile all packages.
build:
	mkdir -p bin/
	go build -buildvcs=false -ldflags "$(GO_LDFLAGS)" -o ./bin/nusashell ./cmd/nusashell

## test: run the full test suite (with race detector when cgo is available;
## plain otherwise — e.g. Windows without gcc).
test:
	@race=""; \
	if [ "$$(go env CGO_ENABLED)" = "1" ] && go env CC >/dev/null 2>&1 && [ -n "$$(go env CC)" ] && command -v "$$(go env CC)" >/dev/null 2>&1; then \
		race="-race"; \
	else \
		echo "make test: cgo/C compiler unavailable, falling back to go test without -race"; \
	fi; \
	go test $$race ./...

## test-frontend: syntax-check the native frontend modules (Node, dev-only).
test-frontend:
	@fail=0; for f in $$(find frontend/js -name '*.js'); do \
		node --check "$$f" || fail=1; \
	done; \
	if [ "$$fail" -eq 1 ]; then echo "frontend: syntax check failed"; exit 1; fi; \
	node --test scripts/agent-instructions.test.mjs; \
	echo "frontend: syntax ok"

## test-frontend-e2e: one cross-layer UI smoke flow against a real Go server.
test-frontend-e2e:
	node --test frontend/tests/e2e.test.mjs

## race: run race-enabled tests.
race:
	go test -race ./...

## vet: static analysis.
vet:
	go vet ./...

## fmt: format all Go source files in place.
fmt:
	find . -path './.git' -prune -o -path './.experimental' -prune -o -type f -name '*.go' -exec gofmt -w {} +
	find . -path './.git' -prune -o -path './.experimental' -prune -o -type f -name '*.go' -exec gofmt -l {} +
	@echo "gofmt: done"

## check: full verification baseline.
check: fmt fmt-check test vet build

## verify-local: run native repository gates plus Windows/macOS compile checks.
verify-local:
	bash ./scripts/verify-local.sh

## hooks: enable the repository-managed pre-push hook for this clone.
hooks:
	git config --local core.hooksPath .githooks
	@echo "Git hooks enabled: .githooks"

## fmt-check: fail when any Go file is not gofmt-formatted.
fmt-check:
	@out="$$(find . -path './.git' -prune -o -path './.experimental' -prune -o -type f -name '*.go' -exec gofmt -l {} +)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt: the following files are not formatted:"; \
		echo "$$out"; \
		exit 1; \
	fi
	@echo "gofmt: ok"

## run: build and start the development server (listens on NUSASHELL_PORT/9999).
run: scan-ui-docs gen-catalog build
	./bin/nusashell

## go-dev: alias for the native Go development server.
go-dev: run

## electron-version: print the Electron wrapper release version.
electron-version:
	@node scripts/version.mjs read

## go-version: print the Go core release version.
go-version:
	@node scripts/version.mjs read-go

## electron-version-sync: update apps/electron package and lock metadata.
electron-version-sync:
	@node scripts/version.mjs sync

## electron-version-check: fail when Electron metadata differs from apps/electron/VERSION.
electron-version-check:
	@node scripts/version.mjs check

## electron-install: install the pinned Electron wrapper dependencies.
electron-install: electron-version-check
	npm ci --prefix apps/electron

## electron-test: run Electron wrapper unit tests without starting a GUI.
electron-test: electron-install
	npm --prefix apps/electron test

## electron-installer-test: validate installer syntax and release metadata.
electron-installer-test:
	node --test scripts/version.test.mjs scripts/release-changes.test.mjs scripts/release-index.test.mjs scripts/release-manifest.test.mjs scripts/release-notes.test.mjs scripts/release-workflow.test.mjs scripts/install.test.mjs

## electron-ui-test: launch the real Electron renderer and exercise web UI flows.
electron-ui-test: electron-build-backend electron-install
	npm --prefix apps/electron run test:ui

## electron-build-backend: stage the current-platform Go backend for Electron
## development and renderer tests. The staged binary is ignored and is never
## copied into a release package.
electron-build-backend:
	mkdir -p apps/electron/runtime
	go build -buildvcs=false -ldflags "$(GO_LDFLAGS)" -o ./apps/electron/runtime/nusashell ./cmd/nusashell

## electron-dev: run the web UI inside Electron with disk-backed frontend assets.
electron-dev: electron-build-backend electron-install
	NUSASHELL_DEV=1 npm --prefix apps/electron run dev

## electron-package: create an unpacked Electron wrapper directory.
## The Go backend is intentionally not embedded; dev/UI targets stage it only
## for the local process they launch.
electron-package: electron-install
	npm --prefix apps/electron run package:dir

## electron-install-local: package and install Electron under the user profile.
electron-install-local: electron-package
	@case "$$(uname -s)" in \
		MINGW*|MSYS*|CYGWIN*) powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/install-local.ps1;; \
		*) bash scripts/install-local.sh;; \
	esac

## electron-dist: create native Electron wrapper artifacts for this platform.
electron-dist: electron-install
	npm --prefix apps/electron run dist

## electron-release-linux: create the standalone Electron payload used by the
## optional Electron part of install.sh on Linux.
electron-release-linux: electron-package
	@case "$$(uname -s)" in \
		Linux) ;; \
		*) echo "electron-release-linux must run on Linux" >&2; exit 1;; \
	esac
	@version="$$(tr -d '\r\n' < "$(ELECTRON_VERSION_FILE)")"; \
	mkdir -p apps/electron/dist/release; \
	tar -C apps/electron/dist/linux-unpacked -czf "$$(pwd)/apps/electron/dist/release/nusashell-electron-$${version}-linux-x64.tar.gz" .; \
	sha256sum "apps/electron/dist/release/nusashell-electron-$${version}-linux-x64.tar.gz" > "apps/electron/dist/release/nusashell-electron-$${version}-linux-x64.tar.gz.sha256"

## electron-release-manifest: index locally produced Electron payloads.
electron-release-manifest: electron-version-check
	node scripts/release-manifest.mjs "$(ELECTRON_VERSION)" apps/electron/dist/release apps/electron/dist/release/electron-latest.json electron

## go-release: package the Go core for the current Unix platform.
## GitHub Actions uses native runners for Windows/macOS packaging; this target
## is useful for a local release smoke test and for Linux distribution.
go-release: build
	@set -e; \
	case "$$(uname -s)" in \
	  Linux) os=linux ;; \
	  Darwin) os=darwin ;; \
	  *) echo "go-release supports Linux/macOS locally; use the native CI job on Windows" >&2; exit 1 ;; \
	esac; \
	case "$$(uname -m)" in x86_64|amd64) arch=x64;; arm64|aarch64) arch=arm64;; *) echo "Unsupported CPU architecture: $$(uname -m)" >&2; exit 1;; esac; \
	version="$$(tr -d '\r\n' < VERSION)"; \
	release_dir="$$(pwd)/release/go"; stage="$$(mktemp -d "$${TMPDIR:-/tmp}/nusashell-go-release.XXXXXX")"; \
	trap 'rm -rf "$$stage"' EXIT; \
	mkdir -p "$$release_dir" "$$stage"; \
	cp ./bin/nusashell "$$stage/nusashell"; \
	tar -C "$$stage" -czf "$$release_dir/nusashell-$${version}-$${os}-$${arch}.tar.gz" nusashell; \
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$$release_dir/nusashell-$${version}-$${os}-$${arch}.tar.gz" > "$$release_dir/nusashell-$${version}-$${os}-$${arch}.tar.gz.sha256"; else shasum -a 256 "$$release_dir/nusashell-$${version}-$${os}-$${arch}.tar.gz" > "$$release_dir/nusashell-$${version}-$${os}-$${arch}.tar.gz.sha256"; fi; \
	echo "Wrote $$release_dir/nusashell-$${version}-$${os}-$${arch}.tar.gz"

## go-release-manifest: index locally produced Go core payloads.
go-release-manifest:
	node scripts/release-manifest.mjs "$(NUSASHELL_VERSION)" release/go release/go/latest.json go

## release-index-check: validate the independent release stream pointer file.
release-index-check:
	node --input-type=module -e "import { readFile } from 'node:fs/promises'; import { validateReleaseIndex } from './scripts/release-index.mjs'; validateReleaseIndex(JSON.parse(await readFile('release-versions.json', 'utf8')));"

## install: build and install the `nusashell` CLI into ~/.local/bin so the
## Go app is runnable as `nusashell` from anywhere. Override the destination
## with NUSASHELL_INSTALL_DIR. The Electron desktop installer uses the separate
## `nusashell-desktop` launcher, so both entrypoints can coexist.
install: build
	@dest="$${NUSASHELL_INSTALL_DIR:-$${HOME}/.local/bin}"; \
	mkdir -p "$$dest"; \
	install -m 0755 ./bin/nusashell "$$dest/nusashell"; \
	echo "installed: $$dest/nusashell"; \
	echo "run: nusashell"

## install-release: execute the cross-platform release installer. The Go core
## is always installed; optional Electron/MCP choices can be supplied through
## NUSASHELL_INSTALL_ELECTRON and NUSASHELL_INSTALL_MCP.
install-release:
	@case "$$(uname -s)" in \
	  MINGW*|MSYS*|CYGWIN*) powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/install.ps1 ;; \
	  *) bash scripts/install.sh ;; \
	esac

## scan-ui-docs: regenerate resources/agent/docs/ui-*.md from ui-map.json.
## Fails when a data-view lacks a map entry or a mapped control ID is missing from source.
scan-ui-docs:
	go run ./cmd/scan-ui-docs

## scan-ui-docs-check: fail if committed ui-*.md differ from generated (drift gate).
scan-ui-docs-check:
	go run ./cmd/scan-ui-docs -check

## gen-catalog: regenerate infrastructure/config/catalog_gen.go from models.dev + openrouter.
gen-catalog:
	go run ./cmd/gen-catalog

## gen-catalog-check: verify catalog_gen.go parses (upstream data changes
## frequently, so this checks validity, not byte-exact freshness).
gen-catalog-check:
	go run ./cmd/gen-catalog -check 2>/dev/null || echo "gen-catalog: stale (expected — upstream data updates frequently)"
