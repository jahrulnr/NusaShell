// Package regex implements service.SymbolIndex with nothing but stdlib
// regular expressions. It is the zero-dependency fallback adapter: always
// Available, slower to express intent but impossible to leave the port
// empty. Extraction is deliberately approximate — brace- and indent-based
// container tracking, no real grammar — so kinds and owners are
// best-effort, not exact. The contract suite pins only the subset both
// adapters must agree on.
package regex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"regexp"
	"strings"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
)

// Index is the regex-backed SymbolIndex. It keeps a per-root content-hash
// cache like the richer adapters so repeated Index calls are cheap.
type Index struct {
	rules    map[string]*langRule
	maxBytes int64
	cache    map[svc.SymbolRoot]*rootCache
}

type rootCache struct {
	hash map[string][32]byte
	syms map[string][]svc.Symbol
}

// defRule matches one line to a symbol. name is the capture group holding
// the identifier, owner (optional) the group holding an explicit owner name
// (a Go method receiver). container marks rules whose match also opens an
// owner scope for member lines below it.
type defRule struct {
	re        *regexp.Regexp
	kind      string
	name      int
	owner     int
	container bool
}

// langRule describes one language: the extensions it claims, its definition
// patterns, and how scopes close (braces or significant indentation).
type langRule struct {
	exts    []string
	indent  bool
	comment *regexp.Regexp
	defs    []defRule
	// memberKinds are promoted from "function" to "method" when emitted
	// inside a container scope.
	members bool
}

func rx(s string) *regexp.Regexp { return regexp.MustCompile(s) }

var langRules = map[string]*langRule{
	"go": {
		exts: []string{".go"},
		defs: []defRule{
			{rx(`^func\s+\(\s*\w+\s+\*?(\w+)\s*\)\s*(\w+)`), "method", 2, 1, false},
			{rx(`^func\s+(\w+)`), "function", 1, 0, false},
			{rx(`^type\s+(\w+)\s+struct\b`), "struct", 1, 0, true},
			{rx(`^type\s+(\w+)\s+interface\b`), "interface", 1, 0, true},
			{rx(`^type\s+(\w+)\s*=?\s*\w`), "type", 1, 0, false},
		},
	},
	"python": {
		exts:   []string{".py"},
		indent: true,
		defs: []defRule{
			{rx(`^(\s*)class\s+(\w+)`), "class", 2, 0, true},
			{rx(`^(\s*)(?:async\s+)?def\s+(\w+)`), "function", 2, 0, false},
		},
		members: true,
	},
	"javascript": {exts: []string{".js", ".mjs", ".cjs"}, defs: jsRules},
	"typescript": {exts: []string{".ts"}, defs: append(append([]defRule{}, jsRules...), tsRules...)},
	"tsx":        {exts: []string{".tsx", ".jsx"}, defs: append(append([]defRule{}, jsRules...), tsRules...)},
	"rust": {
		exts: []string{".rs"},
		defs: []defRule{
			{rx(`^\s*(?:unsafe\s+)?impl(?:<[^>]*>)?(?:\s+[\w:]+\s+for)?\s+([\w:]+)`), "impl", 1, 0, true},
			{rx(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?fn\s+(\w+)`), "function", 1, 0, false},
			{rx(`^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+(\w+)`), "struct", 1, 0, true},
			{rx(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+(\w+)`), "enum", 1, 0, true},
			{rx(`^\s*(?:pub(?:\([^)]*\))?\s+)?trait\s+(\w+)`), "trait", 1, 0, true},
			{rx(`^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+(\w+)`), "module", 1, 0, true},
			{rx(`^\s*macro_rules!\s*(\w+)`), "macro", 1, 0, false},
			{rx(`^\s*(?:pub(?:\([^)]*\))?\s+)?type\s+(\w+)`), "type", 1, 0, false},
			{rx(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:const|static)\s+(\w+)`), "constant", 1, 0, false},
		},
		members: true,
	},
	"java": {
		exts: []string{".java"},
		defs: append(append([]defRule{}, cFamilyTypes...),
			defRule{rx(`^\s*(?:@[\w.()]+\s*)*(?:public|protected|private|static|final|synchronized|abstract|native|strictfp|\s)*[\w<>\[\],.?]+\s+(\w+)\s*\([^;]*\)\s*(?:throws\s+[\w,. ]+)?\s*\{`), "method", 1, 0, false},
		),
		members: true,
	},
	"kotlin": {
		exts: []string{".kt", ".kts"},
		defs: []defRule{
			{rx(`^\s*(?:public|private|internal|protected|open|abstract|sealed|data|enum|inner|\s)*class\s+(\w+)`), "class", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|protected|\s)*interface\s+(\w+)`), "interface", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|protected|\s)*object\s+(\w+)`), "object", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|protected|open|override|suspend|inline|\s)*fun\s+(?:<[^>]+>\s+)?(?:(\w+)\.)?(\w+)`), "function", 2, 1, false},
			{rx(`^\s*(?:public|private|internal|protected|\s)*typealias\s+(\w+)`), "type", 1, 0, false},
		},
		members: true,
	},
	"c_sharp": {
		exts: []string{".cs"},
		defs: append(append([]defRule{
			{rx(`^\s*(?:public|internal|\s)*namespace\s+([\w.]+)`), "module", 1, 0, true},
			{rx(`^\s*(?:public|internal|sealed|abstract|static|partial|readonly|\s)*record\s+(\w+)`), "record", 1, 0, true},
			{rx(`^\s*(?:public|internal|sealed|abstract|static|partial|readonly|\s)*struct\s+(\w+)`), "struct", 1, 0, true},
		}, cFamilyTypes...),
			defRule{rx(`^\s*(?:\[[^\]]*\]\s*)*(?:public|protected|private|internal|static|virtual|override|abstract|async|sealed|readonly|extern|new|partial|\s)*[\w<>\[\],.?]+\s+(\w+)\s*\([^;]*\)\s*(?:where\b[^{]*)?\{?`), "method", 1, 0, false},
		),
		members: true,
	},
	"ruby": {
		exts: []string{".rb"},
		defs: []defRule{
			{rx(`^\s*class\s+([\w:]+)`), "class", 1, 0, true},
			{rx(`^\s*module\s+([\w:]+)`), "module", 1, 0, true},
			{rx(`^\s*def\s+(?:self\.)?(\w+[?!=]?)`), "function", 1, 0, false},
		},
		indent:  true,
		members: true,
	},
	"php": {
		exts: []string{".php"},
		defs: []defRule{
			{rx(`^\s*(?:abstract\s+|final\s+|readonly\s+)*class\s+(\w+)`), "class", 1, 0, true},
			{rx(`^\s*interface\s+(\w+)`), "interface", 1, 0, true},
			{rx(`^\s*trait\s+(\w+)`), "trait", 1, 0, true},
			{rx(`^\s*enum\s+(\w+)`), "enum", 1, 0, true},
			{rx(`^\s*(?:public|protected|private|static|abstract|final|\s)*function\s+&?(\w+)`), "function", 1, 0, false},
			{rx(`^\s*namespace\s+([\w\\]+)`), "module", 1, 0, false},
		},
		members: true,
	},
	"c": {
		exts: []string{".c", ".h"},
		defs: []defRule{
			{rx(`^(?:static\s+|inline\s+|extern\s+)*[\w*]+\s+(\w+)\s*\([^;]*\)\s*\{`), "function", 1, 0, false},
			{rx(`^(?:typedef\s+)?struct\s+(\w+)\s*\{`), "struct", 1, 0, true},
			{rx(`^(?:typedef\s+)?enum\s+(\w+)\s*\{`), "enum", 1, 0, true},
			{rx(`^(?:typedef\s+)?union\s+(\w+)\s*\{`), "struct", 1, 0, true},
			{rx(`^typedef\s+.+?[\s*](\w+)\s*;`), "type", 1, 0, false},
		},
	},
	"cpp": {
		exts: []string{".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
		defs: []defRule{
			{rx(`^\s*(?:inline\s+|template\s*<[^>]*>\s*)*namespace\s+(\w+)`), "module", 1, 0, true},
			{rx(`^\s*(?:template\s*<[^>]*>\s*)?class\s+(\w+)`), "class", 1, 0, true},
			{rx(`^\s*(?:template\s*<[^>]*>\s*)?struct\s+(\w+)`), "struct", 1, 0, true},
			{rx(`^\s*enum\s+(?:class\s+)?(\w+)`), "enum", 1, 0, true},
			{rx(`^\s*(?:template\s*<[^>]*>\s*)?(?:static\s+|inline\s+|virtual\s+|constexpr\s+|extern\s+)*[\w:<>&*]+\s+(\w+)\s*\([^;]*\)\s*(?:const\s*|noexcept\s*|override\s*|final\s*)*\{?`), "function", 1, 0, false},
		},
		members: true,
	},
	"swift": {
		exts: []string{".swift"},
		defs: []defRule{
			{rx(`^\s*(?:public|private|internal|fileprivate|open|final|\s)*class\s+(\w+)`), "class", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|fileprivate|\s)*struct\s+(\w+)`), "struct", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|fileprivate|\s)*enum\s+(\w+)`), "enum", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|fileprivate|\s)*protocol\s+(\w+)`), "interface", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|fileprivate|\s)*extension\s+([\w.]+)`), "impl", 1, 0, true},
			{rx(`^\s*(?:public|private|internal|fileprivate|open|static|class|mutating|override|final|\s)*func\s+(\w+)`), "function", 1, 0, false},
			{rx(`^\s*(?:public|private|internal|fileprivate|\s)*typealias\s+(\w+)`), "type", 1, 0, false},
		},
		members: true,
	},
}

var jsRules = []defRule{
	{rx(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+(\w+)`), "class", 1, 0, true},
	{rx(`^\s*(?:export\s+)?(?:async\s+)?function\*?\s*(\w+)`), "function", 1, 0, false},
	{rx(`^\s*(?:export\s+)?(?:const|let|var)\s+(\w+)(?:\s*:\s*[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[\w$]+)\s*=>`), "function", 1, 0, false},
	{rx(`^\s*(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*function`), "function", 1, 0, false},
	{rx(`^\s*(?:public|private|protected|static|async|readonly|get|set|override|abstract|#?\s)*#?(\w+)\s*\([^)]*\)\s*[:{]?`), "method", 1, 0, false},
}

var tsRules = []defRule{
	{rx(`^\s*(?:export\s+)?interface\s+(\w+)`), "interface", 1, 0, true},
	{rx(`^\s*(?:export\s+)?type\s+(\w+)\s*=`), "type", 1, 0, false},
	{rx(`^\s*(?:export\s+)?enum\s+(\w+)`), "enum", 1, 0, true},
	{rx(`^\s*(?:export\s+)?(?:declare\s+)?(?:namespace|module)\s+([\w.]+)`), "module", 1, 0, true},
}

var cFamilyTypes = []defRule{
	{rx(`^\s*(?:@[\w.()]+\s*)*(?:public|protected|private|internal|static|final|sealed|abstract|partial|new|\s)*record\s+(\w+)`), "record", 1, 0, true},
	{rx(`^\s*(?:@[\w.()]+\s*)*(?:public|protected|private|internal|static|final|sealed|abstract|partial|new|\s)*class\s+(\w+)`), "class", 1, 0, true},
	{rx(`^\s*(?:@[\w.()]+\s*)*(?:public|protected|private|internal|static|\s)*interface\s+(\w+)`), "interface", 1, 0, true},
	{rx(`^\s*(?:@[\w.()]+\s*)*(?:public|protected|private|internal|\s)*@?interface\s+(\w+)`), "interface", 1, 0, true},
	{rx(`^\s*(?:@[\w.()]+\s*)*(?:public|protected|private|internal|\s)*enum\s+(\w+)`), "enum", 1, 0, true},
}

var keywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "else": true, "do": true, "try": true, "finally": true,
	"synchronized": true, "using": true, "lock": true, "foreach": true,
}

// New returns the fallback adapter. maxFileBytes overrides the shared
// skip policy's size cap; zero selects symbol.MaxFileBytes.
func New(maxFileBytes int64) *Index {
	if maxFileBytes <= 0 {
		maxFileBytes = symbol.MaxFileBytes
	}
	return &Index{rules: langRules, maxBytes: maxFileBytes, cache: map[svc.SymbolRoot]*rootCache{}}
}

var _ svc.SymbolIndex = (*Index)(nil)
var _ svc.SymbolIndexAvailability = (*Index)(nil)
var _ svc.SymbolIndexScope = (*Index)(nil)
var _ svc.SymbolIndexDetail = (*Index)(nil)

func (i *Index) Available(svc.SymbolRoot) error { return nil }

// IndexesFile reports whether the rules table claims rel — the same test
// the indexer applies per file.
func (i *Index) IndexesFile(rel string) bool {
	return langOf(i.rules, rel) != ""
}

// Index walks the root and extracts symbols line by line. Extraction is
// sequential and deterministic: breadth-first file order, symbols in
// source order.
func (i *Index) Index(ctx context.Context, root svc.SymbolRoot) ([]svc.Symbol, error) {
	syms, _, _, err := i.IndexDetail(ctx, root)
	return syms, err
}

// IndexDetail is Index plus coverage. A canceled context still returns
// the completed files' symbols along with the error.
func (i *Index) IndexDetail(ctx context.Context, root svc.SymbolRoot) ([]svc.Symbol, int, int, error) {
	var files []string
	if err := root.Walk(ctx, func(rel string, size int64) error {
		if !symbol.ShouldIndexWithin(rel, size, i.maxBytes) {
			return nil
		}
		if langOf(i.rules, rel) == "" {
			return nil
		}
		files = append(files, rel)
		return nil
	}); err != nil {
		return nil, 0, 0, err
	}
	symbol.OrderFiles(files)

	prev := i.cache[root]
	next := &rootCache{hash: map[string][32]byte{}, syms: map[string][]svc.Symbol{}}
	var out []svc.Symbol
	indexed := 0
	var werr error
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			werr = err
			break
		}
		src, err := root.ReadFile(ctx, rel)
		if err != nil {
			continue
		}
		indexed++
		sum := sumOf(src)
		next.hash[rel] = sum
		if prev != nil && prev.hash[rel] == sum {
			next.syms[rel] = prev.syms[rel]
			out = append(out, prev.syms[rel]...)
			continue
		}
		ss := scan(rel, langOf(i.rules, rel), i.rules[langOf(i.rules, rel)], src)
		next.syms[rel] = ss
		out = append(out, ss...)
	}
	i.cache[root] = next
	return out, indexed, len(files), werr
}

func langOf(rules map[string]*langRule, rel string) string {
	for name, r := range rules {
		for _, ext := range r.exts {
			if strings.HasSuffix(rel, ext) {
				return name
			}
		}
	}
	return ""
}

func sumOf(b []byte) [32]byte { return sha256.Sum256(b) }

// scan applies one language's rules to a source. It tracks container scopes
// either by brace depth or by leading indentation, whichever the language
// uses, and promotes function-kinds to "method" inside a container when the
// language marks members.
func scan(rel, lang string, r *langRule, src []byte) []svc.Symbol {
	type scope struct {
		name  string
		depth int // brace body depth, or indent width
	}
	var scopes []scope
	depth := 0
	inBlockComment := false
	var out []svc.Symbol

	owner := func() string {
		if len(scopes) == 0 {
			return ""
		}
		return scopes[len(scopes)-1].name
	}

	lines := bufio.NewScanner(bytes.NewReader(src))
	lines.Buffer(make([]byte, 0, 64<<10), 1<<20)
	lineNo := 0
	for lines.Scan() {
		lineNo++
		raw := lines.Text()
		line := raw
		// crude comment stripping — good enough for definition lines
		if inBlockComment {
			if k := strings.Index(line, "*/"); k >= 0 {
				line = line[k+2:]
				inBlockComment = false
			} else {
				continue
			}
		}
		if k := strings.Index(line, "/*"); k >= 0 {
			if e := strings.Index(line[k+2:], "*/"); e >= 0 {
				line = line[:k] + line[k+2+e+2:]
			} else {
				line = line[:k]
				inBlockComment = true
			}
		}
		if k := strings.Index(line, "//"); k >= 0 {
			line = line[:k]
		}
		if lang == "python" || lang == "ruby" {
			if k := strings.Index(line, "#"); k >= 0 {
				line = line[:k]
			}
		}

		if r.indent {
			ind := len(raw) - len(strings.TrimLeft(raw, " \t"))
			for len(scopes) > 0 && ind <= scopes[len(scopes)-1].depth && strings.TrimSpace(line) != "" {
				scopes = scopes[:len(scopes)-1]
			}
		} else {
			for len(scopes) > 0 && depth < scopes[len(scopes)-1].depth {
				scopes = scopes[:len(scopes)-1]
			}
		}

		for _, d := range r.defs {
			m := d.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name := m[d.name]
			if name == "" || keywords[name] {
				break
			}
			kind := d.kind
			own := ""
			if d.owner > 0 {
				own = m[d.owner]
			}
			if own == "" && d.kind == "function" && r.members && len(scopes) > 0 {
				kind = "method"
				own = owner()
			}
			s := svc.Symbol{Path: rel, Lang: lang, Kind: kind, Name: name, Owner: own, Line: lineNo,
				Public: symbol.Visibility(lang, line, name)}
			out = append(out, s)
			if d.container {
				if r.indent {
					ind := len(raw) - len(strings.TrimLeft(raw, " \t"))
					scopes = append(scopes, scope{name: name, depth: ind})
				} else {
					scopes = append(scopes, scope{name: name, depth: depth + 1})
				}
			}
			break
		}

		if !r.indent {
			for _, c := range line {
				switch c {
				case '{':
					depth++
				case '}':
					depth--
				}
			}
			// pop scopes whose brace closed on this line
			for len(scopes) > 0 && depth < scopes[len(scopes)-1].depth {
				scopes = scopes[:len(scopes)-1]
			}
		}
	}
	// grouped `export { a, b }` names can't be seen line-locally
	if lang == "javascript" || lang == "typescript" || lang == "tsx" {
		exported := symbol.ExportedNames(src)
		for k := range out {
			if !out[k].Public && exported[out[k].Name] {
				out[k].Public = true
			}
		}
	}
	return out
}
