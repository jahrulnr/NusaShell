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

	"gopkg.in/yaml.v3"
)

// Block marshals v as YAML and wraps it in YAML front matter delimited
// by --- lines (Jekyll/Hugo style). Used by all built-in tool handlers
// for consistent, readable output.
func Block(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("---\n# marshal error: %v\n---", err)
	}
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---"
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
