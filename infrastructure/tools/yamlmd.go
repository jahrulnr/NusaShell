package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/pkg/yamlmd"
)

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
		return yamlmd.Block(meta)
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
	return yamlmd.MD(meta, strings.Join(lines, "\n"))
}

func capJSONL(tool string, meta map[string]any, items []any) string {
	if len(items) == 0 {
		return yamlmd.Block(meta)
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
