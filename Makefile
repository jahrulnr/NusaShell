# NusaShell Light (Go port) developer tooling.
#
# Gates follow the repository verification baseline:
# gofmt, go test, go test -race, go vet, go build.

.PHONY: all build test race vet fmt check run test-frontend test-frontend-e2e

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
run: build
	./bin/nusashell
