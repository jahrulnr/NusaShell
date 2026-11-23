package toolfile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

// searchRepo is a git worktree with ignored build output and
// dependencies beside the source.
func searchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for name, data := range map[string]string{
		".gitignore":              "node_modules/\nbuild/\n*.log\n",
		"main.go":                 "package main\n\nfunc main() { Serve() }\n",
		"server/serve.go":         "package server\n\n// Serve starts it.\nfunc Serve() {}\n",
		"web/app.ts":              "export const serve = () => Serve();\n",
		"node_modules/x/index.js": "function Serve() {}\n",
		"build/out.go":            "func Serve() {}\n",
		"debug.log":               "Serve failed\n",
		"assets/logo.png":         "\x89PNG\r\n\x1a\n\x00\x00Serve",
	} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return root
}

func runSearch(t *testing.T, root, input string) string {
	t.Helper()
	tool, err := NewFileSearchTool(repoworkspace.NewLocal(), root)
	if err != nil {
		t.Fatal(err)
	}
	call, _ := entity.NewToolCall("s1", "search", input, enum.TooltypeOrigin)
	res, err := tool.Execute(context.Background(), call, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Text
}

func TestSearchSkipsWhatGitIgnores(t *testing.T) {
	root := searchRepo(t)
	out := runSearch(t, root, `{"pattern":"Serve\\(\\)"}`)
	for _, want := range []string{"main.go:3: func main() { Serve() }", "server/serve.go:4: func Serve() {}", "web/app.ts:1:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, noise := range []string{"node_modules", "build/", "debug.log", "logo.png"} {
		if strings.Contains(out, noise) {
			t.Errorf("ignored or binary %q searched:\n%s", noise, out)
		}
	}
	if !strings.HasSuffix(out, "[search: 3 matches in 5 files searched]") {
		t.Errorf("summary: %s", out)
	}
}

func TestSearchByGlobPathAndCase(t *testing.T) {
	root := searchRepo(t)
	if out := runSearch(t, root, `{"glob":"*.go"}`); out != "main.go\nserver/serve.go\n[search: 2 files]" {
		t.Fatalf("glob by name:\n%s", out)
	}
	if out := runSearch(t, root, `{"glob":"server/**"}`); out != "server/serve.go\n[search: 1 files]" {
		t.Fatalf("glob by path:\n%s", out)
	}
	if out := runSearch(t, root, `{"glob":"*.{ts,go}","pattern":"serve","ignore_case":true,"path":"web"}`); !strings.HasPrefix(out, "web/app.ts:1:") {
		t.Fatalf("path, braces and case:\n%s", out)
	}
	if out := runSearch(t, root, `{"pattern":"Serve","path":"server/serve.go"}`); !strings.HasPrefix(out, "server/serve.go:3: // Serve starts it.") {
		t.Fatalf("a file path searches that file:\n%s", out)
	}
	if out := runSearch(t, root, `{"pattern":"Serve","max_results":1}`); !strings.Contains(out, "[search: first 1 matches;") {
		t.Fatalf("limit:\n%s", out)
	}
	if out := runSearch(t, root, `{"pattern":"nothing-here"}`); out != "no matches" {
		t.Fatalf("no match: %s", out)
	}
}
