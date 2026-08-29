package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/projectmemory"
)

func TestExecuteMemoryProjectRequiresWorkspace(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.ProjectMemory = projectmemory.New(t.TempDir(), nil)
	_, err := tb.Execute(context.Background(), "memory_project", []byte(`{"op":"list"}`))
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("expected workspace error, got %v", err)
	}
}

func TestExecuteMemoryProjectSkipWritesNothing(t *testing.T) {
	data := t.TempDir()
	ws := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.ProjectMemory = projectmemory.New(data, nil)
	ctx := application.WithWorkspace(context.Background(), ws)
	out, err := tb.Execute(ctx, "memory_project", []byte(`{"op":"skip","reason":"implementation only"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status: skipped") {
		t.Fatalf("skip output = %s", out)
	}
	keyDir := filepath.Join(data, domain.ProjectMemoryDirName, domain.ProjectMemoryKey(ws))
	if _, err := os.Stat(keyDir); err == nil {
		t.Fatal("skip must not create a memory directory")
	}
}

func TestExecuteMemoryProjectAdmitAndQuery(t *testing.T) {
	data := t.TempDir()
	ws := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.ProjectMemory = projectmemory.New(data, nil)
	ctx := application.WithWorkspace(context.Background(), ws)
	body := `ID: BUG-port
KIND: DEBUG
SCOPE: fixture port
SYMPTOM: health check hit the wrong port
ROOT_CAUSE: hardcoded 8080
FIX: use the bound port
VALIDATION_COMMAND: true
REUSE: shortens local deploy diagnosis
SINCE: 2026-07-24
`
	out, err := tb.Execute(ctx, "memory_project", []byte(`{"op":"admit","kind":"debug","id":"BUG-port","content":`+jsonQuote(body)+`}`))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if !strings.Contains(out, "status: admitted") {
		t.Fatalf("admit output = %s", out)
	}
	out, err = tb.Execute(ctx, "memory_project", []byte(`{"op":"query","id":"BUG-port"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "BUG-port") {
		t.Fatalf("query output = %s", out)
	}
}

func TestExecuteMemoryProjectAdmitLintRollback(t *testing.T) {
	data := t.TempDir()
	ws := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	st := projectmemory.New(data, nil)
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.ProjectMemory = st
	ctx := application.WithWorkspace(context.Background(), ws)
	good := `ID: BUG-ok
KIND: DEBUG
SCOPE: ok
SYMPTOM: x
ROOT_CAUSE: y
FIX: z
VALIDATION_COMMAND: true
REUSE: r
SINCE: 2026-07-24
`
	if _, err := tb.Execute(ctx, "memory_project", []byte(`{"op":"admit","kind":"debug","content":`+jsonQuote(good)+`}`)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, domain.ProjectMemoryDirName, domain.ProjectMemoryKey(ws), "debug.md")
	before, _ := os.ReadFile(path)
	bad := `ID: BUG-bad
KIND: DEBUG
SCOPE: bad
TOPICS: [Deploy, too-many, topics, here]
`
	_, err := tb.Execute(ctx, "memory_project", []byte(`{"op":"admit","kind":"debug","content":`+jsonQuote(bad)+`}`))
	if err == nil {
		t.Fatal("expected lint failure")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("lint failure changed the file")
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
