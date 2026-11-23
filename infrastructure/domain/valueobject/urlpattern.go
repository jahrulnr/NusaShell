package valueobject

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// GlobURL compiles a URL pattern. A pattern is a glob unless regex is set:
// '**' matches zero or more characters including '/', '*' matches zero or
// more characters except '/', '?' matches exactly one character except '/'.
// The pattern is anchored to the whole URL.
func GlobURL(pattern string, regex bool) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, errors.New("pattern is required")
	}
	if regex {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %v", err)
		}
		return re, nil
	}
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '\\':
			// Backslash escapes the next character so a literal '*' or '?'
			// can appear in a pattern; a trailing backslash is literal.
			if i+1 < len(runes) {
				i++
				b.WriteString(regexp.QuoteMeta(string(runes[i])))
				continue
			}
			b.WriteString(regexp.QuoteMeta("\\"))
		default:
			b.WriteString(regexp.QuoteMeta(string(runes[i])))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	return re, nil
}
