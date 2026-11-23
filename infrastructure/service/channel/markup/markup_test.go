package markup

import "testing"

func TestTelegramHTML(t *testing.T) {
	md := "# Plan\n\n**Bold** and *it* with `a < b` and ~~old~~.\n- one\n- [docs](https://x.dev/a?b=1&c=2)\n\n```go\nif a < b {}\n```\n> quoted *line*\nsnake_case_name stays"
	want := "<b>Plan</b>\n\n<b>Bold</b> and <i>it</i> with <code>a &lt; b</code> and <s>old</s>.\n• one\n• <a href=\"https://x.dev/a?b=1&amp;c=2\">docs</a>\n\n<pre><code class=\"language-go\">if a &lt; b {}</code></pre>\n<blockquote>quoted <i>line</i></blockquote>\nsnake_case_name stays"
	if got := Render(md, TelegramHTML); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWhatsApp(t *testing.T) {
	md := "## Done\n**Tests** pass, see [CI](https://ci.example/run/1) and `make test`.\n```\nok\n```"
	want := "*Done*\n*Tests* pass, see CI (https://ci.example/run/1) and `make test`.\n```ok```"
	if got := Render(md, WhatsApp); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
