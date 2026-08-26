package tools

// Native find_file tool: glob pattern matching for file paths, with
// ** recursive matching and brace expansion. Pure Go — no external
// dependencies.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const findFileMaxResults = 500

type findFileArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func executeFindFile(argsJSON []byte) (bool, string, error) {
	var args findFileArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return true, "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return true, "", fmt.Errorf("pattern is required")
	}
	root := args.Path
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	info, err := os.Stat(root)
	if err != nil {
		return true, "", fmt.Errorf("path not found: %w", err)
	}
	if !info.IsDir() {
		return true, "", fmt.Errorf("path must be a directory, got file: %s", root)
	}

	patterns := expandBraces(args.Pattern)
	var matches []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(matches) >= findFileMaxResults {
			return filepath.SkipDir
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)
		for _, p := range patterns {
			if globMatch(p, relPath) || globMatch(p, filepath.Base(relPath)) {
				matches = append(matches, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		return true, "", err
	}
	sort.Strings(matches)
	return true, yamlBlock(map[string]any{"files": matches, "count": len(matches)}), nil
}

// expandBraces expands {a,b,c} patterns into multiple glob patterns.
// e.g. "*.{go,ts}" → ["*.go", "*.ts"]
func expandBraces(pattern string) []string {
	start := strings.Index(pattern, "{")
	if start == -1 {
		return []string{pattern}
	}
	end := strings.Index(pattern[start:], "}")
	if end == -1 {
		return []string{pattern}
	}
	end += start
	prefix := pattern[:start]
	suffix := pattern[end+1:]
	alternatives := strings.Split(pattern[start+1:end], ",")
	var result []string
	for _, alt := range alternatives {
		expanded := prefix + strings.TrimSpace(alt) + suffix
		result = append(result, expandBraces(expanded)...)
	}
	return result
}
