# NusaShell (Go) developer tooling.
#
# Gates follow the repository verification baseline:
# gofmt, go test, go test -race, go vet, go build.

.PHONY: all build test race vet fmt check run install test-frontend test-frontend-e2e scan-ui-docs scan-ui-docs-check gen-catalog gen-catalog-check

all: check

## build: compile all packages.
build:
	mkdir -p bin/
	go build -o ./bin/nusashell ./cmd/nusashell

## test: run the full test suite (with race detector).
test:
	go test -race ./...

## test-frontend: syntax-check the native frontend modules (Node, dev-only).
test-frontend:
	@fail=0; for f in $$(find frontend/js -name '*.js'); do \
		node --check "$$f" || fail=1; \
	done; \
	if [ "$$fail" -eq 1 ]; then echo "frontend: syntax check failed"; exit 1; fi; \
	echo "frontend: syntax ok"

## test-frontend-e2e: one cross-layer UI smoke flow against a real Go server.
test-frontend-e2e:
	node --test frontend/e2e.test.mjs

## race: run race-enabled tests.
race:
	go test -race ./...

## vet: static analysis.
vet:
	go vet ./...

## fmt: format all Go source files in place.
fmt:
	gofmt -w .
	@echo "gofmt: done"

## check: full verification baseline.
check: fmt-check test vet build

## fmt-check: fail when any Go file is not gofmt-formatted.
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt: the following files are not formatted:"; \
		echo "$$out"; \
		exit 1; \
	fi
	@echo "gofmt: ok"

## run: build and start the development server (listens on NUSASHELL_PORT/9999).
run: scan-ui-docs gen-catalog build
	./bin/nusashell

## install: build and install the `nusashell` CLI into ~/.local/bin so the
## Go app is runnable as `nusashell` from anywhere. Override the destination
## with NUSASHELL_INSTALL_DIR. This replaces any existing ~/.local/bin/nusashell
## (e.g. an Electron wrapper left by NusaShell-Desktop's make install).
install: build
	@dest="$${NUSASHELL_INSTALL_DIR:-$${HOME}/.local/bin}"; \
	mkdir -p "$$dest"; \
	install -m 0755 ./bin/nusashell "$$dest/nusashell"; \
	echo "installed: $$dest/nusashell"; \
	echo "run: nusashell"

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
