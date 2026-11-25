.PHONY: build build-lean serve install test check check-full fmt-check test-web tags provider-add memory-eval docs-serve docs-build

# The build version comes from the VERSION file. A checkout without one builds
# as "dev", and "dev" turns the update check off.
VERSION ?= $(or $(shell tr -d '[:space:]' < VERSION 2>/dev/null),dev)

# repomap + the grammar_subset tags carry the tree-sitter symbol index
# into the default binary (~+13 MB). build-lean drops them for hosts that
# would rather not pay for the symbol_map record — a release does not: the
# published binary is the full one, so `make tags` is what the workflow
# reads instead of keeping a second copy of this list.
REPOMAP_TAGS := repomap grammar_subset grammar_subset_go grammar_subset_javascript grammar_subset_typescript grammar_subset_tsx grammar_subset_python grammar_subset_rust grammar_subset_java grammar_subset_kotlin grammar_subset_c_sharp grammar_subset_ruby grammar_subset_php grammar_subset_c grammar_subset_cpp grammar_subset_swift

# tags prints the build tag set for callers that build the binary
# themselves — the release workflow, above all, so the symbol engine cannot
# silently drop out of what users download.
tags:
	@echo $(REPOMAP_TAGS)

build:
	mkdir -p ./bin
	go build -tags '$(REPOMAP_TAGS)' -ldflags "-X main.version=$(VERSION)" -o bin/nusashell ./cmd/nusashell

build-lean:
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

# One read-only gate over the whole tree: host build, vet, tests (including
# the tagged symbol-map engine), the WebUI syntax check, and a
# cross-compile of every package for the platforms the roadmap claims.
# Separate from `test` because `test` runs `go fix`, which rewrites
# sources — a check that mutates its subject cannot report on it.
# A failing platform names itself in the output; `GOOS=X go build ./...`
# leaves no binary behind because it covers more than one package.
check: fmt-check test-web
	go build ./...
	go vet ./...
	go vet -tags memoryeval ./cmd/nusashell/
	go test ./...
	go test -tags '$(REPOMAP_TAGS)' ./infrastructure/service/symbol/... ./infrastructure/service/tool/symbolmap/ ./infrastructure/config/ ./cmd/nusashell/
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

# check-full runs the same gate without Go's test cache (-count=1). Use it before a
# commit; plain `check` is for the inner loop, where a cached pass is fine.
check-full: fmt-check test-web
	go build ./...
	go vet ./...
	go vet -tags memoryeval ./cmd/nusashell/
	go test -count=1 ./...
	go test -count=1 -tags '$(REPOMAP_TAGS)' ./infrastructure/service/symbol/... ./infrastructure/service/tool/symbolmap/ ./infrastructure/config/ ./cmd/nusashell/
	GOOS=windows go build ./...
	GOOS=darwin go build ./...
