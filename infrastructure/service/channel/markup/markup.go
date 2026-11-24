// Package markup renders the Markdown an agent writes into the limited
// markup a chat surface displays. Each channel supplies a Style; anything a
// style cannot show degrades to plain text rather than failing the send.
package markup

import (
	"regexp"
	"strings"
)

// Style says how one surface writes each construct. Text is called on every
// run of plain text (escaping, if the surface needs it); the other funcs
// receive already-rendered inner text unless noted.
type Style struct {
	Text    func(s string) string
	Bold    func(s string) string
	Italic  func(s string) string
	Strike  func(s string) string
	Code    func(raw string) string       // raw, unescaped
	Pre     func(raw, lang string) string // raw, unescaped
	Link    func(text, url string) string // text rendered, url raw
	Heading func(s string) string         // a "# heading" line
	Quote   func(lines []string) string   // rendered "> " lines
	Bullet  string                        // replaces "- " / "* " list markers
}

var (
	fenceRe   = regexp.MustCompile("^\\s*(```|~~~)\\s*([\\w+-]*)\\s*$")
	headingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	bulletRe  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	quoteRe   = regexp.MustCompile(`^\s{0,3}>\s?(.*)$`)
	ruleRe    = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
)

// Render converts Markdown to the style's markup.
func Render(md string, st Style) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == m[1] {
					break
				}
				body = append(body, lines[j])
			}
			out = append(out, st.Pre(strings.Join(body, "\n"), m[2]))
			i = j
			continue
		}
		if quoteRe.MatchString(line) {
			var q []string
			for ; i < len(lines); i++ {
				m := quoteRe.FindStringSubmatch(lines[i])
				if m == nil {
					break
				}
				q = append(q, inline(m[1], st))
			}
			i--
			out = append(out, st.Quote(q))
			continue
		}
		switch {
		case ruleRe.MatchString(line):
			out = append(out, st.Text("———"))
		case headingRe.MatchString(line):
			out = append(out, st.Heading(inline(headingRe.FindStringSubmatch(line)[1], st)))
		case bulletRe.MatchString(line):
			m := bulletRe.FindStringSubmatch(line)
			out = append(out, st.Text(m[1])+st.Bullet+inline(m[2], st))
		default:
			out = append(out, inline(line, st))
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// inline renders one line's spans: code first (its content is literal),
// then links, bold, strike, and italic in the remaining text.
func inline(s string, st Style) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(s, '`')
		if start < 0 {
			b.WriteString(spans(s, st))
			return b.String()
		}
		end := strings.IndexByte(s[start+1:], '`')
		if end < 0 {
			b.WriteString(spans(s, st))
			return b.String()
		}
		b.WriteString(spans(s[:start], st))
		b.WriteString(st.Code(s[start+1 : start+1+end]))
		s = s[start+2+end:]
	}
}

var (
	linkRe   = regexp.MustCompile(`!?\[([^\]]*)\]\(\s*(?:<([^<>\n]+)>|([^\s()<>]+))\s*\)`)
	webRe    = regexp.MustCompile(`^https?://`)
	boldRe   = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	strikeRe = regexp.MustCompile(`~~([^~\n]+?)~~`)
	italicRe = regexp.MustCompile(`(^|[^\w*])\*([^*\s][^*\n]*?)\*($|[^\w*])|(^|[^\w])_([^_\s][^_\n]*?)_($|[^\w])`)
)

// spans renders text with no code spans in it.
func spans(s string, st Style) string {
	if s == "" {
		return ""
	}
	if loc := linkRe.FindStringSubmatchIndex(s); loc != nil {
		// A link or an image: a web address is a link (an image is never
		// embedded); a local path the reply did not send as a file reads
		// as its label with the path in code, not as raw Markdown.
		label, dest := sub(s, loc, 1), sub(s, loc, 2)
		if dest == "" {
			dest = sub(s, loc, 3)
		}
		var link string
		switch {
		case webRe.MatchString(dest):
			if label == "" {
				label = dest
			}
			link = st.Link(spans(label, st), dest)
		case label == "" || label == dest:
			link = st.Code(dest)
		default:
			link = spans(label, st) + st.Text(" (") + st.Code(dest) + st.Text(")")
		}
		return spans(s[:loc[0]], st) + link + spans(s[loc[1]:], st)
	}
	if loc := boldRe.FindStringSubmatchIndex(s); loc != nil {
		inner := sub(s, loc, 1)
		if inner == "" {
			inner = sub(s, loc, 2)
		}
		return spans(s[:loc[0]], st) + st.Bold(spans(inner, st)) + spans(s[loc[1]:], st)
	}
	if loc := strikeRe.FindStringSubmatchIndex(s); loc != nil {
		return spans(s[:loc[0]], st) + st.Strike(spans(sub(s, loc, 1), st)) + spans(s[loc[1]:], st)
	}
	if loc := italicRe.FindStringSubmatchIndex(s); loc != nil {
		pre, inner, post := sub(s, loc, 1), sub(s, loc, 2), sub(s, loc, 3)
		if loc[4] < 0 {
			pre, inner, post = sub(s, loc, 4), sub(s, loc, 5), sub(s, loc, 6)
		}
		return spans(s[:loc[0]], st) + st.Text(pre) + st.Italic(spans(inner, st)) + st.Text(post) + spans(s[loc[1]:], st)
	}
	return st.Text(s)
}

func sub(s string, loc []int, group int) string {
	if 2*group+1 >= len(loc) || loc[2*group] < 0 {
		return ""
	}
	return s[loc[2*group]:loc[2*group+1]]
}
