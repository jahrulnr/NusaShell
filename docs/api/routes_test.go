package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// legacyRouteDocDebt is the ratchet, not permission: each entry is an API
// surface that was mounted before the documentation rule existed. A new entry
// is a design failure; an entry leaves this list only by being documented in
// openapi.yaml. The test fails when an entry becomes documented or unmounted,
// so the list cannot rot into a second, invisible contract.
var legacyRouteDocDebt = map[string]string{
	"POST /api/memories":                      "pre-existing memory creation surface; documentation debt",
	"DELETE /api/memories/{}":                 "pre-existing memory deletion surface; documentation debt",
	"POST /api/memories/{}/actions":           "pre-existing memory action surface; documentation debt",
	"GET /api/memory-suggestions":             "pre-existing memory-suggestion list surface; documentation debt",
	"POST /api/memory-suggestions/{}/actions": "pre-existing memory-suggestion decision surface; documentation debt",
}

// intentionallyAbsent is the contract's negative space. These are not
// exceptions for mounted routes: they must stay unmounted and undocumented
// because the contract gives their verbs to another operation.
var intentionallyAbsent = map[string]string{
	"POST /api/conversations": "conversation creation is the first turn.send upsert, not a separate POST",
	"PATCH /api/providers/{}": "provider mutation is full replacement through PUT, not a soft PATCH",
}

func TestMountedRoutesAreDocumented(t *testing.T) {
	mounted := scanMountedAPIRoutes(t)
	documented := documentedOperations(t)

	var failures []string
	for key, sources := range mounted {
		if _, ok := documented[key]; ok {
			if reason, excepted := legacyRouteDocDebt[key]; excepted {
				failures = append(failures, fmt.Sprintf("%s is documented but still excepted (%s); remove the exception", key, reason))
			}
			continue
		}
		if _, excepted := legacyRouteDocDebt[key]; excepted {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s is mounted at %s but has no documented operation", key, strings.Join(sources, ", ")))
	}
	for key, reason := range legacyRouteDocDebt {
		if _, ok := mounted[key]; !ok {
			failures = append(failures, fmt.Sprintf("stale route exception %s (%s); the route is no longer mounted", key, reason))
		}
	}
	for key, reason := range intentionallyAbsent {
		if sources, ok := mounted[key]; ok {
			failures = append(failures, fmt.Sprintf("%s is mounted at %s but the contract forbids it: %s", key, strings.Join(sources, ", "), reason))
		}
		if _, ok := documented[key]; ok {
			failures = append(failures, fmt.Sprintf("%s is documented but the contract forbids it: %s", key, reason))
		}
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("route completeness failures:\n- %s", strings.Join(failures, "\n- "))
	}
}

func scanMountedAPIRoutes(t *testing.T) map[string][]string {
	t.Helper()
	root := filepath.Join("..", "..", "infrastructure", "delivery", "http")
	mounted := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !httpMethods[sel.Sel.Name] {
				return true
			}
			receiver, ok := sel.X.(*ast.Ident)
			if !ok || receiver.Name != "api" {
				return true
			}
			if len(call.Args) == 0 {
				t.Errorf("%s: %s route registration has no path argument", path, sel.Sel.Name)
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: %s route registration must use a string literal so the route audit can see it", path, sel.Sel.Name)
				return true
			}
			route, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: bad route literal %s: %v", path, lit.Value, err)
				return true
			}
			key := routeKey(sel.Sel.Name, "/api"+route)
			mounted[key] = append(mounted[key], filepath.ToSlash(path))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounted) == 0 {
		t.Fatal("no mounted API routes found; the scanner is not reading the delivery tree")
	}
	return mounted
}

func documentedOperations(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	out := map[string]bool{}
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		for method := range ops {
			upper := strings.ToUpper(method)
			if httpMethods[upper] {
				out[routeKey(upper, path)] = true
			}
		}
	}
	return out
}

func routeKey(method, path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") || (strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")) {
			parts[i] = "{}"
		}
	}
	return method + " " + strings.Join(parts, "/")
}
