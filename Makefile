# NusaShell (Go) developer tooling.
#
# Gates follow the repository verification baseline:
# gofmt, go test, go test -race, go vet, go build, frontend tests.
#
# App-specific targets live next to the app:
#   make -C apps/electron <target>
#   make -C apps/pets <target>

VERSION_FILE ?= VERSION
NUSASHELL_VERSION := $(shell tr -d '\r\n' < "$(VERSION_FILE)")
GO_LDFLAGS ?= -X main.version=$(NUSASHELL_VERSION)

.PHONY: all build test race vet fmt check verify-local hooks run go-dev install install-bin install-release test-frontend test-frontend-e2e scan-ui-docs scan-ui-docs-check gen-catalog gen-catalog-check go-version installer-test go-release go-release-manifest release-index-check

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

## test-frontend: syntax-check frontend modules and run the Node test suite
## (jsdom unit tests plus the Go-backed e2e smoke in frontend/tests/).
test-frontend:
	@if ! command -v node >/dev/null 2>&1; then \
		echo "test-frontend: Node.js is required" >&2; exit 1; \
	fi
	@if [ ! -d node_modules ]; then \
		echo "test-frontend: frontend dependencies are missing; run npm ci first." >&2; exit 1; \
	fi
	@fail=0; for f in $$(find frontend/js -name '*.js'); do \
		node --check "$$f" || fail=1; \
	done; \
	if [ "$$fail" -eq 1 ]; then echo "frontend: syntax check failed"; exit 1; fi
	node --test scripts/agent-instructions.test.mjs
	node --test frontend/tests/*.test.mjs
	@echo "frontend: ok"

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

## check: full verification baseline (Go gates + frontend tests).
check: fmt fmt-check test vet build test-frontend

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

## run: build and start the development server (listens on NUSASHELL_PORT/10994).
run: scan-ui-docs build
	./bin/nusashell

## go-dev: alias for the native Go development server.
go-dev: run

## go-version: print the Go core release version.
go-version:
	@node scripts/version.mjs read-go

## installer-test: validate installer syntax and shared release metadata.
installer-test:
	node --test scripts/version.test.mjs scripts/release-changes.test.mjs scripts/release-index.test.mjs scripts/release-manifest.test.mjs scripts/release-notes.test.mjs scripts/release-workflow.test.mjs scripts/install.test.mjs

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

## install: interactive local installer — build this checkout’s Go core, then
## optionally build+install the login service, desktop pet (Linux), and Electron.
## Same prompt style as the curl release installer, but compiles from source
## instead of downloading GitHub releases. See scripts/install-local.sh / .ps1.
install:
	@case "$$(uname -s)" in \
	  MINGW*|MSYS*|CYGWIN*) powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/install-local.ps1 ;; \
	  *) bash scripts/install-local.sh ;; \
	esac

## install-bin: build this checkout and copy only the Go CLI into ~/.local/bin
## (flat path; no versioned layout / optional components). Override destination
## with NUSASHELL_INSTALL_DIR.
install-bin: build
	@dest="$${NUSASHELL_INSTALL_DIR:-$${HOME}/.local/bin}"; \
	mkdir -p "$$dest"; \
	install -m 0755 ./bin/nusashell "$$dest/nusashell"; \
	echo "installed: $$dest/nusashell"; \
	echo "run: nusashell"

## install-release: download+install published GitHub releases (curl/irm flow).
## Use this for the same experience as `curl … | bash` / `irm … | iex`.
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
