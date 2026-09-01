package text

import (
	"strings"
	"testing"
)

func TestTruncate_short(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
}

func TestTruncate_long(t *testing.T) {
	got := Truncate("hello world", 5)
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("expected prefix preserved, got %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("expected ellipsis marker, got %q", got)
	}
}

func TestTruncate_zeroN(t *testing.T) {
	if got := Truncate("hello", 0); got != "hello" {
		t.Fatalf("n=0 should return input unchanged, got %q", got)
	}
}

func TestTruncateRunes_short(t *testing.T) {
	if got := TruncateRunes("hello", 10); got != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
}

func TestTruncateRunes_long(t *testing.T) {
	got := TruncateRunes("hello world", 5)
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("expected prefix preserved, got %q", got)
	}
	if !strings.Contains(got, "[truncated:") {
		t.Fatalf("expected omission marker, got %q", got)
	}
}

func TestTruncateWithNote_short(t *testing.T) {
	if got := TruncateWithNote("hello", 10); got != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
}

func TestTruncateWithNote_long(t *testing.T) {
	got := TruncateWithNote("hello world this is long", 5)
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("expected prefix preserved, got %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncated note, got %q", got)
	}
}

func TestVisible_trimsWhitespace(t *testing.T) {
	if got := Visible("  hello  "); got != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
}

func TestVisible_empty(t *testing.T) {
	if got := Visible("\n\n"); got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestPersistable_empty(t *testing.T) {
	if got := Persistable("  \n\n  "); got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestPersistable_keepsTrailingSpace(t *testing.T) {
	got := Persistable("  hello world  \n")
	if got != "hello world  " {
		t.Fatalf("got %q want %q", got, "hello world  ")
	}
}

func TestPersistable_trimsLeadingWhitespace(t *testing.T) {
	got := Persistable("\n\n  hello")
	if got != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
}
