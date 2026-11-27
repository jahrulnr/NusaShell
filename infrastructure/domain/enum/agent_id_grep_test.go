package enum

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Behaviour asks the runtime kind. An empty agent id is how a binding is
// built, and those lines say so. Everything else is a caller deciding
// behaviour from the identifier.
func TestNoBehaviouralAgentIDEmptiness(t *testing.T) {
	root := moduleRoot(t)
	goCheck := regexp.MustCompile(`AgentID(\(\))?\s*(!=|==)\s*""`)
	webCheck := regexp.MustCompile(`(?:!!|!)(?:sel|s|base|pend|room\.info\?|r\.info\?|info\?|selection\([^)]*\))\.agentId\b|(?:sel|s|base|pend|pending\?|r\.info\?|room\.info\?|info\?)\.agentId\s*\?`)
	var bad []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
			bad = append(bad, unmarked(t, rel, path, goCheck)...)
		case rel == "web/lib/chat.js" || rel == "web/components/pickers.js" || rel == "web/components/transcript.js":
			bad = append(bad, unmarked(t, rel, path, webCheck)...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("behaviour still reads an empty agent id:\n%s", strings.Join(bad, "\n"))
	}
}

func unmarked(t *testing.T, rel, path string, re *regexp.Regexp) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	for i, line := range strings.Split(string(body), "\n") {
		if !re.MatchString(line) || strings.Contains(line, "binding-constructor") {
			continue
		}
		// A web line that already consulted the server's runtime is naming an
		// identifier, not deciding the kind.
		if strings.HasSuffix(rel, ".js") && strings.Contains(line, "runtime") {
			continue
		}
		bad = append(bad, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
	}
	return bad
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
