package symbol

import (
	"regexp"
	"strings"
	"unicode"
)

// reWord matches a keyword as a whole word on a line.
func reWord(kw string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\w])` + kw + `(?:[^\w]|$)`)
}

var (
	reExport   = reWord(`export`)
	rePub      = reWord(`pub`)
	rePublic   = reWord(`public`)
	reInternal = reWord(`internal`)
	reProtect  = reWord(`protected`)
	rePrivate  = reWord(`private`)
	reFilepriv = reWord(`fileprivate`)
	reStatic   = reWord(`static`)
	reExportBr = regexp.MustCompile(`\bexport\s*\{([^}]*)\}`)
)

// Visibility reports whether a definition line makes its symbol outwardly
// visible, per language convention. It is the single visibility heuristic
// for all adapters; a language with no signal returns false rather than
// guessing. line is the source line the definition starts on.
func Visibility(lang, line, name string) bool {
	switch lang {
	case "go":
		r, _ := utf8First(name)
		return unicode.IsUpper(r)
	case "python":
		return !strings.HasPrefix(name, "_")
	case "ruby":
		return !strings.HasPrefix(name, "_")
	case "rust":
		return rePub.MatchString(line)
	case "javascript", "typescript", "tsx":
		return reExport.MatchString(line)
	case "java", "c_sharp":
		if rePrivate.MatchString(line) {
			return false
		}
		return rePublic.MatchString(line) || reProtect.MatchString(line) ||
			reInternal.MatchString(line)
	case "php":
		return !rePrivate.MatchString(line)
	case "kotlin", "swift":
		if rePrivate.MatchString(line) || reFilepriv.MatchString(line) {
			return false
		}
		// default visibility is module-public in both languages
		return true
	case "c", "cpp":
		return !reStatic.MatchString(line)
	}
	return false
}

func utf8First(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}

// ExportedNames returns identifiers listed in `export { a, b as c }` blocks —
// the export form a single def line cannot see. JS-family adapters apply it
// as a second pass over the file.
func ExportedNames(src []byte) map[string]bool {
	out := map[string]bool{}
	for _, m := range reExportBr.FindAllSubmatch(src, -1) {
		for item := range strings.SplitSeq(string(m[1]), ",") {
			item = strings.TrimSpace(item)
			if i := strings.Index(item, " as "); i >= 0 {
				item = strings.TrimSpace(item[i+4:])
			}
			if item != "" {
				out[item] = true
			}
		}
	}
	return out
}
