package tools

// Native grep tool: regex search across files with glob filtering,
// context lines, and structured output modes. Pure Go — no external
// ripgrep dependency required.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	grepDefaultMaxResults = 100
	grepMaxFileBytes      = 10 << 20 // skip files larger than 10 MB
	grepMaxContextLines   = 10
	// grepMaxOutputChars caps the formatted result size. max_results caps the
	// match count but not the bytes: a few matches on huge lines (minified
	// JS, base64 blobs, giant JSON lines) can produce multi-megabyte output
	// that alone exceeds the model's context window and defeats compaction.
	grepMaxOutputChars = 200_000
)

type grepArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	GlobPattern     string `json:"glob_pattern"`
	OutputMode      string `json:"output_mode"`
	ContextLines    int    `json:"context_lines"`
	CaseInsensitive bool   `json:"case_insensitive"`
	MaxResults      int    `json:"max_results"`
}

func executeGrep(argsJSON []byte) (bool, string, error) {
	var args grepArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return true, "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return true, "", fmt.Errorf("pattern is required")
	}
	if strings.TrimSpace(args.Path) == "" {
		return true, "", fmt.Errorf("path is required")
	}

	mode := args.OutputMode
	if mode == "" {
		mode = "content"
	}
	switch mode {
	case "content", "files_with_matches", "count":
	default:
		return true, "", fmt.Errorf("output_mode must be content, files_with_matches, or count (got %q)", mode)
	}

	contextLines := args.ContextLines
	if contextLines < 0 {
		contextLines = 0
	}
	if contextLines > grepMaxContextLines {
		contextLines = grepMaxContextLines
	}

	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = grepDefaultMaxResults
	}

	pat := args.Pattern
	if args.CaseInsensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return true, "", fmt.Errorf("invalid regex: %w", err)
	}

	info, err := os.Stat(args.Path)
	if err != nil {
		return true, "", fmt.Errorf("path not found: %w", err)
	}

	var matches []grepMatch
	if !info.IsDir() {
		matches, err = grepFile(args.Path, re, contextLines)
		if err != nil {
			return true, "", err
		}
	} else {
		matches, err = grepDir(args.Path, args.GlobPattern, re, contextLines, maxResults)
		if err != nil {
			return true, "", err
		}
	}

	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	out := formatGrepResults(matches, mode)
	if len(out) > grepMaxOutputChars {
		omitted := len(out) - grepMaxOutputChars
		out = truncateGrepOutput(out, grepMaxOutputChars)
		out += fmt.Sprintf("\n... [output truncated: %d chars omitted — narrow the pattern, reduce context_lines, or use output_mode files_with_matches/count]", omitted)
	}
	return true, out, nil
}

type grepMatch struct {
	File    string
	Line    int
	Content string
	Context []grepContextLine
}

type grepContextLine struct {
	Line    int
	Content string
	Before  bool
}

func grepDir(root, glob string, re *regexp.Regexp, contextLines, maxResults int) ([]grepMatch, error) {
	var matches []grepMatch
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if len(matches) >= maxResults {
			return filepath.SkipDir
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" && !globMatch(glob, path) {
			return nil
		}
		fileMatches, err := grepFile(path, re, contextLines)
		if err != nil {
			return nil // skip unreadable files
		}
		matches = append(matches, fileMatches...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	return matches, nil
}

func grepFile(path string, re *regexp.Regexp, contextLines int) ([]grepMatch, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > grepMaxFileBytes {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var matches []grepMatch
	for i, line := range lines {
		if re.MatchString(line) {
			m := grepMatch{File: path, Line: i + 1, Content: line}
			if contextLines > 0 {
				for c := 1; c <= contextLines; c++ {
					if i-c >= 0 {
						m.Context = append(m.Context, grepContextLine{Line: i - c + 1, Content: lines[i-c], Before: true})
					}
					if i+c < len(lines) {
						m.Context = append(m.Context, grepContextLine{Line: i + c + 1, Content: lines[i+c]})
					}
				}
			}
			matches = append(matches, m)
		}
	}
	return matches, nil
}

func formatGrepResults(matches []grepMatch, mode string) string {
	if len(matches) == 0 {
		return yamlBlock(map[string]any{"matches": 0})
	}
	switch mode {
	case "files_with_matches":
		seen := make(map[string]bool)
		var files []string
		for _, m := range matches {
			if !seen[m.File] {
				seen[m.File] = true
				files = append(files, m.File)
			}
		}
		return yamlBlock(map[string]any{"files": files, "count": len(files)})
	case "count":
		counts := make(map[string]int)
		for _, m := range matches {
			counts[m.File]++
		}
		var files []string
		for f := range counts {
			files = append(files, f)
		}
		sort.Strings(files)
		items := make([]any, 0, len(files))
		for _, f := range files {
			items = append(items, map[string]any{"file": f, "matches": counts[f]})
		}
		return yamlJSONL(map[string]any{"total_matches": len(matches)}, items)
	default: // content
		items := make([]any, 0, len(matches))
		for _, m := range matches {
			item := map[string]any{"file": m.File, "line": m.Line, "content": m.Content}
			if len(m.Context) > 0 {
				var ctx []map[string]any
				for _, c := range m.Context {
					entry := map[string]any{"line": c.Line, "content": c.Content}
					if c.Before {
						entry["before"] = true
					}
					ctx = append(ctx, entry)
				}
				item["context"] = ctx
			}
			items = append(items, item)
		}
		return yamlJSONL(map[string]any{"matches": len(matches)}, items)
	}
}

// truncateGrepOutput keeps the first n runes of s without splitting a
// multi-byte character (grep output is UTF-8 text).
func truncateGrepOutput(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// globMatch implements glob matching with ** support.
func globMatch(pattern, name string) bool {
	// Normalize separators
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)

	// If pattern contains **, expand to a recursive walk match
	if strings.Contains(pattern, "**") {
		return doubleStarMatch(pattern, name)
	}

	matched, err := filepath.Match(pattern, filepath.Base(name))
	if err != nil || !matched {
		// Try matching the full path
		matched, err = filepath.Match(pattern, name)
		if err != nil || !matched {
			return false
		}
	}
	return true
}

// doubleStarMatch matches a path against a pattern containing **.
// ** matches any number of path segments (including zero).
func doubleStarMatch(pattern, name string) bool {
	patSegs := strings.Split(pattern, "/")
	nameSegs := strings.Split(name, "/")
	return matchSegs(patSegs, nameSegs)
}

func matchSegs(patSegs, nameSegs []string) bool {
	if len(patSegs) == 0 {
		return len(nameSegs) == 0
	}
	if patSegs[0] == "**" {
		// ** matches zero or more segments
		for i := 0; i <= len(nameSegs); i++ {
			if matchSegs(patSegs[1:], nameSegs[i:]) {
				return true
			}
		}
		return false
	}
	if len(nameSegs) == 0 {
		return false
	}
	matched, err := filepath.Match(patSegs[0], nameSegs[0])
	if err != nil || !matched {
		return false
	}
	return matchSegs(patSegs[1:], nameSegs[1:])
}
