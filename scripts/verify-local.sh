#!/usr/bin/env bash
# Run the checks that can be performed before pushing without another OS.
# Native tests provide runtime coverage for this machine; the other CI targets
# are compile-checked because their test binaries cannot run on this host.

set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

null_device=/dev/null
if [ "$(go env GOHOSTOS)" = "windows" ]; then
	null_device=NUL
fi

run_step() {
	local label="$1"
	shift

	printf '\n[verify-local] %s\n' "$label"
	"$@"
}

check_gofmt() {
	local unformatted

	printf '\n[verify-local] Go formatting\n'
	# .experimental/ contains isolated spikes and may carry its own module or
	# formatting policy; it is intentionally outside the repository gates.
	unformatted="$(find . \
		-path './.git' -prune -o \
		-path './.experimental' -prune -o \
		-type f -name '*.go' -print0 | xargs -0 -r gofmt -l)"
	if [ -n "$unformatted" ]; then
		printf 'gofmt: the following files are not formatted:\n%s\n' "$unformatted" >&2
		return 1
	fi
	printf 'gofmt: ok\n'
}

run_native_tests() {
	local cgo_enabled cc

	cgo_enabled="$(go env CGO_ENABLED)"
	cc="$(go env CC)"
	if [ "$cgo_enabled" = "1" ] && [ -n "$cc" ] && command -v "$cc" >/dev/null 2>&1; then
		run_step "native Go tests (-race)" go test -race ./...
		return
	fi

	printf '\n[verify-local] native Go tests (race unavailable)\n'
	printf 'warning: CGO_ENABLED=1 with an available C compiler is required for -race; running plain tests.\n' >&2
	go test ./...
}

run_frontend_tests() {
	if ! command -v node >/dev/null 2>&1; then
		printf 'frontend tests require Node.js 24 or newer.\n' >&2
		return 1
	fi
	if [ ! -d node_modules ]; then
		printf 'frontend dependencies are missing; run npm ci first.\n' >&2
		return 1
	fi
	run_step "frontend tests" node --test frontend/*.test.mjs
}

compile_target() {
	local target_os="$1"
	local target_arch="$2"
	local target="$target_os/$target_arch"
	local packages package_count=0 package_name

	run_step "cross build ($target)" \
		env CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build ./...

	printf '\n[verify-local] cross test compile (%s)\n' "$target"
	packages="$(env CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
		go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)"
	while IFS= read -r package_name; do
		[ -n "$package_name" ] || continue
		package_count=$((package_count + 1))
		if ! env CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
			go test -c -o "$null_device" "$package_name"; then
			printf 'cross test compile failed for %s (%s)\n' "$package_name" "$target" >&2
			return 1
		fi
	done <<EOF
$packages
EOF
	printf 'cross test compile: %s packages ok\n' "$package_count"
}

check_gofmt
run_step "UI documentation drift" go run ./cmd/scan-ui-docs -check
run_step "Go vet" go vet ./...
run_native_tests
run_step "native Go build" go build ./...
run_frontend_tests

# These are the platforms used by the backend CI matrix. Cross-compilation
# catches platform-specific build and _test.go errors, but not runtime behavior.
compile_target windows amd64
compile_target darwin amd64
compile_target darwin arm64

printf '\n[verify-local] all local and cross-platform checks passed\n'
