package domain

import "testing"

func TestAccountKey(t *testing.T) {
	if got := AccountKeyPrefix("codex"); got != "codex:account:" {
		t.Fatalf("AccountKeyPrefix = %q", got)
	}
	if got := AccountKey("codex", "acc-1"); got != "codex:account:acc-1" {
		t.Fatalf("AccountKey = %q", got)
	}
}
