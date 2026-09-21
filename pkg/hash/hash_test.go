package hash

import "testing"

func TestText_sha256(t *testing.T) {
	// SHA-256("hello"), verifiable with `echo -n hello | sha256sum`.
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := Text("hello"); got != want {
		t.Fatalf("Text: got %s want %s", got, want)
	}
}

func TestText_deterministic(t *testing.T) {
	got := Text("hello journal")
	if got == "" {
		t.Fatal("expected non-empty hash")
	}
	if Text("hello journal") != got {
		t.Fatal("hash must be deterministic")
	}
}
