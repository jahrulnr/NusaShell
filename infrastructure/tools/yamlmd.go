package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/internal/yamlmd"
)

// yamlBlock marshals v as YAML and wraps it in YAML front matter delimited
// by --- lines (Jekyll/Hugo style). Used by all built-in tool handlers for
// consistent, readable output.
//
// Thin wrapper around the shared yamlmd.Block so the implementation lives
// in one place (nusashell/internal/yamlmd) and both application/ and
// infrastructure/tools/ share the same code.
func yamlBlock(v any) string {
	return yamlmd.Block(v)
}

// yamlMD produces a YAML front matter block followed by an optional body.
// When body is empty, only the front matter is returned. This is the
// standard output format for all built-in tools.
//
// Thin wrapper around the shared yamlmd.MD so the implementation lives
// in one place (nusashell/internal/yamlmd) and both application/ and
// infrastructure/tools/ share the same code.
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
	return yamlmd.MD(meta, body)
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

func capJSONL(tool string, meta map[string]any, items []any) string {
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
	return capToolOutput(tool, meta, strings.Join(lines, "\n"))
}
