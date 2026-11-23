.PHONY: build serve install test check fmt-check test-web provider-add memory-eval docs-serve docs-build

# The build version comes from the VERSION file. A checkout without one builds
# as "dev", and "dev" turns the update check off.
VERSION ?= $(or $(shell tr -d '[:space:]' < VERSION 2>/dev/null),dev)

build:
	mkdir -p ./bin
	go build -ldflags "-X main.version=$(VERSION)" -o bin/nusashell ./cmd/nusashell

serve: build
	./bin/nusashell serve

install: build
	./bin/nusashell serve install

test: build
	go fix ./...
	go vet ./...
	go test ./...

# One read-only gate over the whole tree: host build, vet, tests, the WebUI
# syntax check, and a cross-compile of every package for the platforms the
# roadmap claims. Separate from `test` because `test` runs `go fix`, which
# rewrites sources — a check that mutates its subject cannot report on it.
# A failing platform names itself in the output; `GOOS=X go build ./...`
# leaves no binary behind because it covers more than one package.
check: fmt-check test-web
	go build ./...
	go vet ./...
	go vet -tags memoryeval ./cmd/nusashell/
	go test ./...
	GOOS=windows go build ./...
	GOOS=darwin go build ./...

fmt-check:
	@files=$$(gofmt -l $$(go list -f '{{.Dir}}' ./... | grep -v /vendor/)); \
	if [ -n "$$files" ]; then echo "$$files"; exit 1; fi

# Syntax-checks the WebUI sources (vendored files excluded), then runs the
# controller and projection tests under Node's built-in runner.
test-web:
	@for f in web/app.js web/lib/*.js web/components/*.js; do node --check "$$f" || exit 1; done
	node --import ./web/tests/register.mjs --test web/tests/*.test.mjs

# Runs the memory-consolidation prompt against the utility model assigned
# in the config `serve` reads (CONFIG_PATH), over the synthetic threads in
# cmd/nusashell/testdata/memory-eval. It calls the real model, so it is
# not part of `test` or `check`; those only compile it.
memory-eval:
	go test -tags memoryeval -run TestMemoryConsolidationEval -count=1 -v ./cmd/nusashell/

docs-serve:
	zensical serve

docs-build:
	zensical build --clean --strict

provider-add: build
	./bin/nusashell provider add \
	opencode --style completion \
	--endpoint https://opencode.ai/zen/v1 \
	--models big-pickle --key-stdin
