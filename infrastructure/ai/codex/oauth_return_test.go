package codex

import (
	"strings"
	"testing"
)

func TestDefaultNusaShellReturnURL(t *testing.T) {
	t.Setenv("NUSASHELL_HOST", "127.0.0.1")
	t.Setenv("NUSASHELL_PORT", "10994")
	got := defaultNusaShellReturnURL()
	if got != "http://127.0.0.1:10994/#providers" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultNusaShellReturnURLMapsWildcardHost(t *testing.T) {
	t.Setenv("NUSASHELL_HOST", "0.0.0.0")
	t.Setenv("NUSASHELL_PORT", "11000")
	got := defaultNusaShellReturnURL()
	if got != "http://127.0.0.1:11000/#providers" {
		t.Fatalf("got %q", got)
	}
}

func TestHTMLEscape(t *testing.T) {
	got := htmlEscape(`http://x/"'><script>`)
	if strings.Contains(got, "<") || strings.Contains(got, `"`) {
		t.Fatalf("unsafe escape: %q", got)
	}
}
