package tools

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlBlock marshals v as YAML and wraps it in YAML front matter delimited
// by --- lines (Jekyll/Hugo style). Used by all built-in tool handlers for
// consistent, readable output.
func yamlBlock(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("---\n# marshal error: %v\n---", err)
	}
	s := strings.TrimRight(string(b), "\n")
	return "---\n" + s + "\n---"
}

// yamlMD produces a YAML×Markdown tool output: a YAML front matter block
// followed by an optional markdown body. When body is empty, only the front
// matter is returned. This is the standard output format for all built-in tools.
//
// Example:
//
//	---
//	count: 3
//	---
//
//	## Items
//	- **id**: abc
func yamlMD(meta any, body string) string {
	block := yamlBlock(meta)
	body = strings.TrimSpace(body)
	if body == "" {
		return block
	}
	return block + "\n\n" + body
}

// yamlMDList is a convenience wrapper for list/search results: it builds
// metadata (at minimum a count) and a markdown list body from the provided
// lines. When lines is empty, only the front matter is returned.
func yamlMDList(meta any, lines []string) string {
	if len(lines) == 0 {
		return yamlBlock(meta)
	}
	body := strings.Join(lines, "\n")
	return yamlMD(meta, body)
}
