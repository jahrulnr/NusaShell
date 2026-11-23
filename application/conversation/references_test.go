package appconversation

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

// A selection rides the user message into history, and the model reads
// what it means after the text.
func TestSendKeepsReferences(t *testing.T) {
	var mu sync.Mutex
	var body string
	app, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(raw)
		mu.Unlock()
		okHandler(w, r)
	})
	text := "look at /main.go now"
	ref, err := valueobject.NewReference("file", "/work/main.go", "/main.go", 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(t.Context(), "ref-room", "gw1", "m1", text, nil, appjob.SendOptions{References: []valueobject.Reference{ref}}, nil); err != nil {
		t.Fatal(err)
	}
	conv, err := app.deps.Convs.Load(t.Context(), "ref-room")
	if err != nil {
		t.Fatal(err)
	}
	got := conv.Messages()[0].References()
	if len(got) != 1 || got[0].ID() != "/work/main.go" || got[0].Kind() != enum.ReferenceFile {
		t.Fatalf("stored references = %+v", got)
	}
	if conv.Messages()[0].Content() != text {
		t.Fatalf("stored text = %q", conv.Messages()[0].Content())
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, "Selected references:") || !strings.Contains(body, "/work/main.go") {
		t.Fatalf("model request does not explain the selection: %s", body)
	}
}

func TestSendRejectsMisplacedReferences(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ref, _ := valueobject.NewReference("file", "/work/main.go", "/main.go", 0, 8)
	_, err := app.Send(t.Context(), "", "gw1", "m1", "look at /main.go", nil, appjob.SendOptions{References: []valueobject.Reference{ref}}, nil)
	if apperror.As(err).StatusCode() != 400 {
		t.Fatalf("err = %v, want 400", err)
	}
}

func TestReferenceCandidates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for name, data := range map[string]string{
		"main.go":          "package main",
		"internal/main.md": "notes",
		"build/main.bin":   "ignored",
		".gitignore":       "build/\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	app, _ := fixture(t, okHandler)
	app.deps.Environments[enum.EnvironmentLocal] = appjob.EnvironmentBundle{WorkspaceStore: repoworkspace.NewLocal(), DefaultRoot: root}
	app.UseReferenceSources(nil, func(context.Context) []string { return []string{"browser", "docs"} })

	items, truncated, err := app.ReferenceCandidates(t.Context(), Place{}, "main")
	if err != nil || truncated {
		t.Fatalf("search: %v truncated=%v", err, truncated)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Kind+":"+it.Detail)
	}
	want := "file:main.go,file:internal/main.md"
	if strings.Join(got, ",") != want {
		t.Fatalf("candidates = %v, want %s (git-ignored build/ left out)", got, want)
	}
	items, _, _ = app.ReferenceCandidates(t.Context(), Place{}, "doc")
	if len(items) != 1 || items[0].Kind != "mcp" || items[0].ID != "docs" {
		t.Fatalf("mcp candidates = %+v", items)
	}
	if _, _, err := app.ReferenceCandidates(t.Context(), Place{Workspace: "relative"}, ""); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("relative workspace err = %v", err)
	}
}
