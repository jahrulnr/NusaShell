package domain

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDomainImportsOnlyStdlibAndPkg enforces the domain layer boundary: every
// non-test .go file in domain/ may import only standard-library packages
// (first path segment without a dot) and nusashell/pkg/* packages. The domain
// layer must not depend on application, contracts, infrastructure, transport,
// UI, or provider SDK code — those are outer layers. This guard prevents
// provider-wire knowledge and other outer-layer concerns from leaking back
// into the domain after they have been moved out.
//
// knownViolations lists pre-existing imports that violate the rule but were
// not moved in this change. Each entry must name the file and the import so
// the violation is tracked, not hidden. The guard still fails on any NEW
// violation not listed here.
func TestDomainImportsOnlyStdlibAndPkg(t *testing.T) {
	// knownViolations: pre-existing third-party imports in domain/ that are
	// out of scope for this refactor. Each must be addressed in a follow-up.
	knownViolations := map[string]map[string]bool{
		"acp_summary.go": {"gopkg.in/yaml.v3": true},
	}

	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q): %v", dir, err)
	}

	fset := token.NewFileSet()
	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: parse error: %v", name, err)
		}
		scanned++
		allowed := knownViolations[name]
		for _, imp := range file.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if isAllowedDomainImport(impPath) {
				continue
			}
			if allowed != nil && allowed[impPath] {
				continue
			}
			t.Errorf("%s: import %q is not allowed in domain (only stdlib and nusashell/pkg/* permitted)", name, impPath)
		}
	}

	if scanned < 10 {
		t.Fatalf("import guard scanned only %d non-test .go files in domain/ — expected at least 10 (guard must not silently no-op)", scanned)
	}
}

func TestIsAllowedDomainImportRejectsOuterNusaShellPackages(t *testing.T) {
	for _, path := range []string{
		"nusashell/application/provider",
		"nusashell/contracts",
		"nusashell/infrastructure/jsonstore",
		"nusashell/transport",
	} {
		if isAllowedDomainImport(path) {
			t.Errorf("isAllowedDomainImport(%q) = true, want false", path)
		}
	}
	if !isAllowedDomainImport("nusashell/pkg/text") {
		t.Error("nusashell/pkg/text must remain allowed")
	}
}

// isAllowedDomainImport reports whether an import path is permitted in the
// domain layer: standard library (first segment has no dot) or a
// nusashell/pkg/* package.
func isAllowedDomainImport(path string) bool {
	if strings.HasPrefix(path, "nusashell/pkg/") {
		return true
	}
	if strings.HasPrefix(path, "nusashell/") {
		return false
	}
	first := path
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		first = path[:idx]
	}
	return !strings.ContainsRune(first, '.')
}
