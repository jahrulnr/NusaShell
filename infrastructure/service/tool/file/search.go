package toolfile

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Bounds on one search.
const (
	searchDefaultResults = 100
	searchMaxResults     = 500
	searchFileMax        = 2 << 20 // larger files are data, not source
	searchLineMax        = 240     // runes of a matched line shown
)

// FileSearchTool finds files by path and text by regular expression in
// the turn's workspace. The walk goes through the workspace repository,
// which leaves out what the tree's version control ignores, so build
// output, dependencies and other ignored trees never flood the answer.
type FileSearchTool struct {
	dir    string
	store  drepo.Workspace
	schema valueobject.ToolSchema
}

// NewFileSearchTool binds the tool's default root; a turn-scoped
// workspace dir overrides it per call.
func NewFileSearchTool(store drepo.Workspace, root string) (service.Tool, error) {
	abs, err := checkDir(store, root)
	if err != nil {
		return nil, err
	}
	schema, err := valueobject.NewToolSchema(
		"search",
		"Search the workspace's files: text by regular expression (RE2 syntax), paths by glob, or both. Leaves out what .gitignore ignores, .git, binary files and files over 2 MiB. Answers path:line: text for each matching line, or the matching paths when no pattern is given.",
		json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regular expression to find in file contents (RE2). Omit to list the files the glob matches"},"glob":{"type":"string","description":"Only files whose path matches, e.g. *.go, src/**/*.ts. Without a slash it matches the file name"},"path":{"type":"string","description":"Folder or file to search, relative to the workspace root or absolute inside one of the thread's folders (default: the workspace root)"},"ignore_case":{"type":"boolean","description":"Match letters in either case"},"max_results":{"type":"integer","description":"Most matches to return — default 100, at most 500"}}}`),
		true,
		enum.ToolPermissionAgent,
	)
	if err != nil {
		return nil, err
	}
	return &FileSearchTool{dir: abs, store: store, schema: schema}, nil
}

func (tool *FileSearchTool) Schema() valueobject.ToolSchema { return tool.schema }

func (tool *FileSearchTool) Execute(ctx context.Context, call entity.ToolCall, _ service.ToolEmitter) (service.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return service.ToolResult{}, err
	}
	var input struct {
		Pattern    string `json:"pattern"`
		Glob       string `json:"glob"`
		Path       string `json:"path"`
		IgnoreCase bool   `json:"ignore_case"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &input); err != nil {
		return service.ToolResult{}, fmt.Errorf("invalid search input: %w", err)
	}
	if input.Pattern == "" && input.Glob == "" {
		return service.ToolResult{}, errors.New("give a pattern, a glob, or both")
	}
	var re *regexp.Regexp
	if input.Pattern != "" {
		expr := input.Pattern
		if input.IgnoreCase {
			expr = "(?i)" + expr
		}
		var err error
		if re, err = regexp.Compile(expr); err != nil {
			return service.ToolResult{}, fmt.Errorf("pattern is not a valid RE2 expression: %w", err)
		}
	}
	var glob *regexp.Regexp
	if input.Glob != "" {
		glob = globRegexp(input.Glob)
	}
	limit := input.MaxResults
	if limit <= 0 {
		limit = searchDefaultResults
	}
	limit = min(limit, searchMaxResults)

	root, rel, err := placeOf(ctx, tool.dir, cmp.Or(input.Path, "."))
	if err != nil {
		return service.ToolResult{}, err
	}
	session, err := tool.store.Open(ctx, root)
	if err != nil {
		return service.ToolResult{}, err
	}
	defer session.Close()
	rel = filepath.Clean(rel)
	// A path naming a file searches that file alone.
	var files []string
	if info, err := session.Stat(rel); err != nil {
		return service.ToolResult{}, fmt.Errorf("%q: %w", input.Path, err)
	} else if !info.IsDir() {
		files = []string{rel}
	}

	var out []string
	matches, scanned, more := 0, 0, false
	consider := func(name string) error {
		if glob != nil && !glob.MatchString(filepath.ToSlash(name)) && !(!strings.Contains(input.Glob, "/") && glob.MatchString(path.Base(filepath.ToSlash(name)))) {
			return nil
		}
		if re == nil {
			if matches == limit {
				more = true
				return fs.SkipAll
			}
			out = append(out, filepath.ToSlash(name))
			matches++
			return nil
		}
		scanned++
		hits, full, err := grepFile(session, name, re, limit-matches)
		if err != nil {
			return nil // unreadable, binary or too large: not text to search
		}
		out = append(out, hits...)
		matches += len(hits)
		if full {
			more = true
			return fs.SkipAll
		}
		return nil
	}
	if files != nil {
		_ = consider(files[0])
	} else if err := tool.store.Entries(ctx, filepath.Join(root, rel), func(name string, isDir bool) error {
		if isDir {
			return nil
		}
		return consider(filepath.Join(rel, name))
	}); err != nil && !errors.Is(err, fs.SkipAll) {
		return service.ToolResult{}, err
	}

	if len(out) == 0 {
		return service.ToolResult{Text: "no matches"}, nil
	}
	var b strings.Builder
	for _, line := range out {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	switch {
	case more && re != nil:
		fmt.Fprintf(&b, "[search: first %d matches; narrow the pattern, glob or path for the rest]", matches)
	case more:
		fmt.Fprintf(&b, "[search: first %d files; narrow the glob or path for the rest]", matches)
	case re != nil:
		fmt.Fprintf(&b, "[search: %d matches in %d files searched]", matches, scanned)
	default:
		fmt.Fprintf(&b, "[search: %d files]", matches)
	}
	return service.ToolResult{Text: b.String()}, nil
}

// grepFile answers up to limit "path:line: text" hits for re in one text
// file, and whether it stopped at the limit with more to find. A binary,
// non-UTF-8 or oversized file is an error: it is not text to search.
func grepFile(session drepo.WorkspaceSession, name string, re *regexp.Regexp, limit int) ([]string, bool, error) {
	f, info, err := session.OpenRegular(name)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if info.Size() > searchFileMax {
		return nil, false, errors.New("too large")
	}
	data, err := io.ReadAll(io.LimitReader(f, searchFileMax+1))
	if err != nil {
		return nil, false, err
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 || !utf8.Valid(data) {
		return nil, false, errors.New("not text")
	}
	var hits []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), searchFileMax)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if !re.MatchString(line) {
			continue
		}
		if len(hits) == limit {
			return hits, true, nil
		}
		line = strings.TrimSpace(line)
		if utf8.RuneCountInString(line) > searchLineMax {
			line = string([]rune(line)[:searchLineMax]) + "…"
		}
		hits = append(hits, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(name), n, line))
	}
	return hits, false, nil
}

// globRegexp turns a glob into an anchored expression over slash paths:
// ** spans folders, * and ? stay within one, {a,b} picks one.
func globRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	glob = filepath.ToSlash(strings.TrimPrefix(glob, "./"))
	depth := 0 // inside {…}
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if strings.HasPrefix(glob[i:], "**/") {
				b.WriteString("(?:.*/)?")
				i += 2
			} else if strings.HasPrefix(glob[i:], "**") {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			depth++
			b.WriteString("(?:")
		case '}':
			if depth == 0 {
				b.WriteString(regexp.QuoteMeta("}"))
				continue
			}
			depth--
			b.WriteString(")")
		case ',':
			if depth == 0 {
				b.WriteString(",")
				continue
			}
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile("^" + regexp.QuoteMeta(glob) + "$")
	}
	return re
}
