// Package api keeps the API documents honest: the OpenAPI file must parse,
// every $ref in it must resolve, and operation ids must be unique.
package api

import (
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestOpenAPIIsConsistent(t *testing.T) {
	raw, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}

	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("openapi.yaml has no paths")
	}

	seen := map[string]string{}
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		for method, op := range ops {
			o, ok := op.(map[string]any)
			if !ok || method == "parameters" {
				continue
			}
			id, _ := o["operationId"].(string)
			if id == "" {
				t.Errorf("%s %s has no operationId", strings.ToUpper(method), path)
				continue
			}
			if prev, dup := seen[id]; dup {
				t.Errorf("operationId %q is used by %s and by %s %s", id, prev, strings.ToUpper(method), path)
			}
			seen[id] = strings.ToUpper(method) + " " + path
		}
	}

	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for key, value := range n {
				if ref, ok := value.(string); ok && key == "$ref" {
					if !resolves(doc, ref) {
						t.Errorf("unresolved $ref %s", ref)
					}
					continue
				}
				walk(value)
			}
		case []any:
			for _, item := range n {
				walk(item)
			}
		}
	}
	walk(doc)
}

// resolves follows a local reference such as #/components/schemas/Name.
func resolves(doc map[string]any, ref string) bool {
	path, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return false
	}
	var cur any = doc
	for part := range strings.SplitSeq(path, "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[part]; !ok {
			return false
		}
	}
	return true
}
