// Package yamlmd renders YAML front-matter × Markdown tool output.
//
// This is a shared leaf package at the module root (under internal/) so
// both application/ and infrastructure/tools/ can import it without
// violating Go's internal package rule or the Clean Architecture
// dependency rule. It depends only on stdlib + gopkg.in/yaml.v3.
package yamlmd

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	clock "nusashell/pkg/time"
)

// Block marshals v as YAML and wraps it in YAML front matter delimited
// by --- lines (Jekyll/Hugo style). Used by all built-in tool handlers
// for consistent, readable output.
func Block(v any) (out string) {
	defer func() {
		if rec := recover(); rec != nil {
			out = fmt.Sprintf("---\n# marshal error: %v\n---", rec)
		}
	}()
	b, err := yaml.Marshal(sanitize(v))
	if err != nil {
		return fmt.Sprintf("---\n# marshal error: %v\n---", err)
	}
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---"
}

// sanitize rewrites values yaml.v3 cannot encode without panicking.
// A *time.Time boxed in interface{} (map[string]any / []any) hits
// encoder.timev, which type-asserts the elem to time.Time and panics
// on both nil and non-nil pointers (gopkg.in/yaml.v3@v3.0.1).
func sanitize(v any) any {
	switch t := v.(type) {
	case *time.Time:
		if t == nil {
			return nil
		}
		return clock.NewTime(*t).RFC3339()
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = sanitize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sanitize(val)
		}
		return out
	default:
		return v
	}
}

// MD produces a YAML front matter block followed by an optional body.
// When body is empty, only the front matter is returned. This is the
// standard output format for all built-in tools.
//
// Example:
//
//	---
//	count: 3
//	---
//
//	## Items
//	- **id**: abc
func MD(meta any, body string) string {
	block := Block(meta)
	body = strings.TrimSpace(body)
	if body == "" {
		return block
	}
	return block + "\n\n" + body
}
