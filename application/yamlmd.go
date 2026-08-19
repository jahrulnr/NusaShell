package application

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlBlockApp marshals v as YAML and wraps it in YAML front matter delimited
// by --- lines. Application-package version of the tools.yamlBlock helper,
// used by read_image / read_audio / read_video tool handlers.
func yamlBlockApp(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("---\n# marshal error: %v\n---", err)
	}
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---"
}

// yamlMDApp produces a YAML×Markdown tool output: a YAML front matter block
// followed by an optional markdown body.
func yamlMDApp(meta any, body string) string {
	block := yamlBlockApp(meta)
	body = strings.TrimSpace(body)
	if body == "" {
		return block
	}
	return block + "\n\n" + body
}
