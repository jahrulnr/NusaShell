package markup

import (
	"html"
	"strings"
)

// TelegramHTML is Telegram's HTML parse mode: b, i, s, code, pre,
// blockquote, and a — every other character escaped.
var TelegramHTML = Style{
	Text:   html.EscapeString,
	Bold:   func(s string) string { return "<b>" + s + "</b>" },
	Italic: func(s string) string { return "<i>" + s + "</i>" },
	Strike: func(s string) string { return "<s>" + s + "</s>" },
	Code:   func(raw string) string { return "<code>" + html.EscapeString(raw) + "</code>" },
	Pre: func(raw, lang string) string {
		if lang != "" {
			return `<pre><code class="language-` + html.EscapeString(lang) + `">` + html.EscapeString(raw) + "</code></pre>"
		}
		return "<pre>" + html.EscapeString(raw) + "</pre>"
	},
	Link:    func(text, url string) string { return `<a href="` + html.EscapeString(url) + `">` + text + "</a>" },
	Heading: func(s string) string { return "<b>" + s + "</b>" },
	Quote:   func(lines []string) string { return "<blockquote>" + strings.Join(lines, "\n") + "</blockquote>" },
	Bullet:  "• ",
}

// WhatsApp is WhatsApp's own markup: *bold*, _italic_, ~strike~, `code`,
// ```blocks```, and "> " quotes. Links are written in full.
var WhatsApp = Style{
	Text:   func(s string) string { return s },
	Bold:   func(s string) string { return "*" + s + "*" },
	Italic: func(s string) string { return "_" + s + "_" },
	Strike: func(s string) string { return "~" + s + "~" },
	Code:   func(raw string) string { return "`" + raw + "`" },
	Pre:    func(raw, _ string) string { return "```" + raw + "```" },
	Link: func(text, url string) string {
		if text == url {
			return url
		}
		return text + " (" + url + ")"
	},
	Heading: func(s string) string { return "*" + s + "*" },
	Quote: func(lines []string) string {
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return strings.Join(lines, "\n")
	},
	Bullet: "• ",
}
