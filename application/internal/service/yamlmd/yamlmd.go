// Package yamlmd renders YAML front-matter × Markdown tool output.
// Extracted from the application root so media tool handlers depend on a
// small leaf package instead of the whole application package.
package yamlmd

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAMLBlock marshals v as YAML and wraps it in YAML front matter delimited
// by --- lines.
func YAMLBlock(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("---\n# marshal error: %v\n---", err)
	}
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---"
}

// YAMLMD produces a YAML×Markdown tool output: a YAML front matter block
// followed by an optional markdown body.
func YAMLMD(meta any, body string) string {
	block := YAMLBlock(meta)
	body = strings.TrimSpace(body)
	if body == "" {
		return block
	}
	return block + "\n\n" + body
}
