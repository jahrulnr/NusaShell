package admin

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The operator reads this line. A wrong-typed value must name the leaf and the
// type it expects, and the sentence lives in the public message now — the
// printer must not echo it again as the cause.
func TestConfigSetTypeMismatchExplainsOnce(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_PATH", configPath)

	var out, errs bytes.Buffer
	if code := Config([]string{"set", "conversation.max_turn_steps", "abc"}, &out, &errs); code != 1 {
		t.Fatalf("exit %d, stderr=%q", code, errs.String())
	}
	got := errs.String()
	if n := regexp.MustCompile(regexp.QuoteMeta(`expects a number value like 512`)).FindAllStringIndex(got, -1); len(n) != 1 {
		t.Fatalf("stderr = %q, want the sentence exactly once (got %d)", got, len(n))
	}
	if bytes.Contains(errs.Bytes(), []byte("caused by:")) {
		t.Fatalf("stderr = %q, want no cause repeated after the message", got)
	}
	if !bytes.Contains(errs.Bytes(), []byte("conversation.max_turn_steps")) {
		t.Fatalf("stderr = %q, want it to name the leaf", got)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}\n" {
		t.Fatalf("a refused set rewrote the config: %q", raw)
	}
}
