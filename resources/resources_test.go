package resources

import (
	"strings"
	"testing"
)

func TestTemplatesEmbedUserAndSoul(t *testing.T) {
	user := Template("user")
	if !strings.Contains(user, "# Overview") {
		t.Fatalf("user.md template missing Overview heading:\n%s", user)
	}
	soul := Template("soul")
	if !strings.Contains(soul, "# About Agent") {
		t.Fatalf("soul.md template missing About Agent heading:\n%s", soul)
	}
}
