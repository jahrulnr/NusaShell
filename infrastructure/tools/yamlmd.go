package tools

import (
	"encoding/json"
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

// yamlMD produces a YAML front matter block followed by an optional body.
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
func yamlMD(meta any, body string) string {
	block := yamlBlock(meta)
	body = strings.TrimSpace(body)
	if body == "" {
		return block
	}
	return block + "\n\n" + body
}

// yamlJSONL produces a YAML front matter block followed by a JSONL body:
// one JSON object per line. Each item in items is marshaled to a single
// line of compact JSON. When items is empty, only the front matter is
// returned. This is the standard format for list/search tool output —
// the agent can parse each line independently and the data is structured,
// not prose.
//
// Example:
//
//	---
//	count: 2
//	---
//
//	{"id":"frag_1","category":"user","content":"prefers Indonesian"}
//	{"id":"frag_2","category":"project","content":"Go + Clean Arch"}
func yamlJSONL(meta any, items []any) string {
	if len(items) == 0 {
		return yamlBlock(meta)
	}
	var lines []string
	for _, item := range items {
		b, err := json.Marshal(item)
		if err != nil {
			lines = append(lines, fmt.Sprintf(`{"error":"marshal: %s"}`, err.Error()))
			continue
		}
		lines = append(lines, string(b))
	}
	return yamlMD(meta, strings.Join(lines, "\n"))
}
