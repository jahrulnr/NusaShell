package tools

// Native grep tool: regex search across files, rendered ripgrep-style
// (file:line:content). Hybrid backend: when the `rg` binary is on PATH we
// shell out to it (gitignore-aware, parallel, battle-tested regex) and parse
// its --json stream; otherwise we fall back to a pure-Go walker so the tool
// stays portable. Both backends feed one shared rg-style formatter, so output
// is identical regardless of which ran. No ripgrep engine is reimplemented
// here — rg does the searching, we only shape the output.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	grepDefaultMaxResults = 100
	grepMaxFileBytes      = 10 << 20 // skip files larger than 10 MB
	grepMaxContextLines   = 10
	// grepMaxLineBytes clips a single match/context line (minified JS,
	// one-line vendor bundles). rg --json does not honor --max-columns, so
	// the formatter applies the cap. The formatted body then goes through
	// capToolOutput (32KiB inline + spill to tmp).
	grepMaxLineBytes = 200
)

type grepArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	GlobPattern     string `json:"glob_pattern"`
	OutputMode      string `json:"output_mode"`
	ContextLines    int    `json:"context_lines"`
	CaseInsensitive bool   `json:"case_insensitive"`
	MaxResults      int    `json:"max_results"`
	ShowWhitespace  bool   `json:"show_whitespace"`
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

	if _, err := os.Stat(args.Path); err != nil {
		return true, "", fmt.Errorf("path not found: %w", err)
	}

	var (
		matches []grepMatch
		via     string
		capped  bool
		total   = -1 // true total of matched lines; -1 = unknown (fallback dir walk stopped early)
	)
	if rgAvailable() {
		ms, cp, tot, err := rgSearch(args, mode, contextLines, maxResults)
		if err != nil {
			return true, "", err
		}
		matches, via, capped, total = ms, "rg", cp, tot
	} else {
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
		if !info.IsDir() {
			matches, err = grepFile(args.Path, re, contextLines)
			total = len(matches) // single file: grepFile returns every match
		} else {
			matches, total, err = grepDir(args.Path, args.GlobPattern, re, contextLines, maxResults)
		}
		if err != nil {
			return true, "", err
		}
		capped = len(matches) >= maxResults
		if len(matches) > maxResults {
			matches = matches[:maxResults]
		}
		via = "go"
	}

	return true, formatRgResults(matches, mode, via, capped, total, args.ShowWhitespace), nil
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

// --- ripgrep backend (shell-out) -------------------------------------------

var (
	rgOnce sync.Once
	rgPath string
)

// rgAvailable reports whether the ripgrep binary is on PATH (checked once).
func rgAvailable() bool {
	rgOnce.Do(func() {
		if p, err := exec.LookPath("rg"); err == nil {
			rgPath = p
		}
	})
	return rgPath != ""
}

// rgJSONLine is the subset of ripgrep's --json records we consume. The final
// stats record carries the true totals even when match collection was capped,
// so capped content results can report total_line_matches.
type rgJSONLine struct {
	Type string `json:"type"`
	Data struct {
		Path  struct{ Text string } `json:"path"`
		Lines struct{ Text string } `json:"lines"`
		Line  int                   `json:"line_number"`
		Stats struct {
			MatchedLines int `json:"matched_lines"`
			Matches      int `json:"matches"`
		} `json:"stats"`
	} `json:"data"`
}

// rgSearch runs ripgrep and parses its --json stream into grepMatch records.
// The second return reports whether the stream hit maxResults (more matches
// exist than were kept); the third carries the true total of matched lines
// from rg's final stats record (-1 when unavailable).
func rgSearch(args grepArgs, mode string, contextLines, maxResults int) ([]grepMatch, bool, int, error) {
	rgArgs := []string{"--json", "--color", "never"}
	if args.CaseInsensitive {
		rgArgs = append(rgArgs, "-i")
	}
	if args.GlobPattern != "" {
		rgArgs = append(rgArgs, "-g", args.GlobPattern)
	}
	// Negative globs last so they win over a positive **/*.js (rg: later
	// glob takes precedence). Mirrors find_file skip of .git/node_modules/vendor.
	rgArgs = append(rgArgs, rgDefaultExcludeGlobs()...)
	if mode == "content" && contextLines > 0 {
		rgArgs = append(rgArgs, "-C", strconv.Itoa(contextLines))
	}
	rgArgs = append(rgArgs, "-e", args.Pattern, "--", args.Path)

	cmd := exec.Command(rgPath, rgArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, -1, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, false, -1, err
	}
	matches, capped, total := parseRgJSON(stdout, contextLines, maxResults)
	waitErr := cmd.Wait()
	if waitErr != nil && len(matches) == 0 {
		if ee, ok := waitErr.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return matches, capped, total, nil // exit 1 == no matches
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return nil, false, -1, fmt.Errorf("rg: %s", msg)
	}
	return matches, capped, total, nil
}

// parseRgJSON consumes ripgrep --json records, grouping context lines with
// their match. It drains the stream fully (so cmd.Wait never deadlocks) but
// stops collecting once maxResults matches are kept; the second return is
// true when that cap was hit. The third return carries the total matched
// lines reported by rg's final stats record (-1 when the stream had none),
// which stays accurate even when collection was capped.
func parseRgJSON(r io.Reader, contextLines, maxResults int) ([]grepMatch, bool, int) {
	var matches []grepMatch
	prevIdx := -1
	var ctxBuf []grepContextLine
	capped := false
	total := -1

	// Attach any trailing context as after-context of the last match.
	flush := func() {
		if prevIdx >= 0 {
			for _, c := range ctxBuf {
				if c.Line > matches[prevIdx].Line {
					matches[prevIdx].Context = append(matches[prevIdx].Context,
						grepContextLine{Line: c.Line, Content: c.Content})
				}
			}
		}
		prevIdx = -1
		ctxBuf = nil
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024) // tolerate very long lines
	for sc.Scan() {
		var rec rgJSONLine
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		switch rec.Type {
		case "stats", "summary":
			if rec.Data.Stats.MatchedLines > 0 {
				total = rec.Data.Stats.MatchedLines
			} else if rec.Data.Stats.Matches > 0 {
				total = rec.Data.Stats.Matches
			}
		case "begin", "end":
			if capped {
				continue // drain only
			}
			flush()
		case "context":
			if capped {
				continue // drain only
			}
			ctxBuf = append(ctxBuf, grepContextLine{
				Line: rec.Data.Line, Content: strings.TrimRight(rec.Data.Lines.Text, "\n"),
			})
		case "match":
			if capped {
				continue // drain only
			}
			L := rec.Data.Line
			m := grepMatch{
				File: rec.Data.Path.Text, Line: L,
				Content: strings.TrimRight(rec.Data.Lines.Text, "\n"),
			}
			for _, c := range ctxBuf {
				switch {
				case prevIdx >= 0 && c.Line > matches[prevIdx].Line && c.Line <= matches[prevIdx].Line+contextLines:
					matches[prevIdx].Context = append(matches[prevIdx].Context,
						grepContextLine{Line: c.Line, Content: c.Content})
				case c.Line >= L-contextLines && c.Line < L:
					m.Context = append(m.Context,
						grepContextLine{Line: c.Line, Content: c.Content, Before: true})
				}
			}
			ctxBuf = nil
			matches = append(matches, m)
			prevIdx = len(matches) - 1
			if len(matches) >= maxResults {
				capped = true
			}
		}
	}
	flush()
	return matches, capped, total
}

// --- shared rg-style formatter ---------------------------------------------

// formatRgResults renders matches ripgrep-style. content: file:line:text with
// context lines using "-" separators; files_with_matches: one path per line;
// count: file:N per line (N = match count in that file). The YAML header
// carries the tallies with unit-labeled keys — line_matches counts matched
// lines (the :N in content rows is a 1-based line number, not a byte
// offset), which backend ran (via: rg|go), and capped: true when
// max_results cut the result short. When capped, totalMatches (>= 0) is
// reported as total_line_matches so the reader knows how many matches
// exist beyond the truncated page; -1 means the true total is unknown.
func formatRgResults(matches []grepMatch, mode, via string, capped bool, totalMatches int, showWhitespace bool) string {
	meta := map[string]any{"via": via}
	if capped {
		meta["capped"] = true
	}
	if len(matches) == 0 {
		meta["line_matches"] = 0
		return yamlBlock(meta)
	}
	var b strings.Builder
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
		for _, f := range files {
			b.WriteString(f)
			b.WriteByte('\n')
		}
		meta["files"] = len(files)
		return capToolOutput("grep", meta, strings.TrimRight(b.String(), "\n"))
	case "count":
		counts := make(map[string]int)
		for _, m := range matches {
			counts[m.File]++
		}
		files := make([]string, 0, len(counts))
		for f := range counts {
			files = append(files, f)
		}
		sort.Strings(files)
		total := 0
		for _, f := range files {
			fmt.Fprintf(&b, "%s:%d\n", f, counts[f])
			total += counts[f]
		}
		meta["files"] = len(files)
		meta["total_line_matches"] = total
		return capToolOutput("grep", meta, strings.TrimRight(b.String(), "\n"))
	default: // content
		for _, m := range matches {
			var before, after []grepContextLine
			for _, c := range m.Context {
				if c.Before {
					before = append(before, c)
				} else {
					after = append(after, c)
				}
			}
			sort.Slice(before, func(i, j int) bool { return before[i].Line < before[j].Line })
			sort.Slice(after, func(i, j int) bool { return after[i].Line < after[j].Line })
			for _, c := range before {
				fmt.Fprintf(&b, "%s-%d-%s\n", m.File, c.Line, clipGrepLine(c.Content, showWhitespace))
			}
			fmt.Fprintf(&b, "%s:%d:%s\n", m.File, m.Line, clipGrepLine(m.Content, showWhitespace))
			for _, c := range after {
				fmt.Fprintf(&b, "%s-%d-%s\n", m.File, c.Line, clipGrepLine(c.Content, showWhitespace))
			}
		}
		meta["line_matches"] = len(matches)
		if capped && totalMatches >= 0 {
			meta["total_line_matches"] = totalMatches
		}
		return capToolOutput("grep", meta, strings.TrimRight(b.String(), "\n"))
	}
}

// --- pure-Go fallback backend ----------------------------------------------

func grepDir(root, glob string, re *regexp.Regexp, contextLines, maxResults int) ([]grepMatch, int, error) {
	var matches []grepMatch
	stopped := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if len(matches) >= maxResults {
			stopped = true
			return filepath.SkipDir
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if grepSkipNoiseFile(d.Name()) {
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
		return nil, -1, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	total := len(matches)
	if stopped {
		total = -1 // walk aborted at maxResults; the real total is unknown
	}
	return matches, total, nil
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

func rgDefaultExcludeGlobs() []string {
	return []string{
		"-g", "!**/.git/**",
		"-g", "!**/node_modules/**",
		"-g", "!**/vendor/**",
		"-g", "!*.min.js",
		"-g", "!*.min.css",
		"-g", "!*.map",
	}
}

func grepSkipNoiseFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".min.js") || strings.HasSuffix(lower, ".min.css") || strings.HasSuffix(lower, ".map")
}

func clipGrepLine(s string, showWhitespace bool) string {
	if showWhitespace {
		s = escapeVisibleWhitespace(s)
	}
	if len(s) <= grepMaxLineBytes {
		return s
	}
	head := clipUTF8Prefix(s, grepMaxLineBytes)
	return head + fmt.Sprintf(" [line omitted, %d chars]", len(s)-len(head))
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
