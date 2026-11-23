package appmcp

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repomcpserver "github.com/jahrulnr/nusashell/infrastructure/repository/mcpserver"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

type fakeRuntime struct {
	mu     sync.Mutex
	loaded [][]string
}

func (f *fakeRuntime) Load(_ context.Context, list []aggregate.MCPServer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, s := range list {
		names = append(names, s.Name())
	}
	f.loaded = append(f.loaded, names)
}

func (f *fakeRuntime) Status() map[string]Status {
	return map[string]Status{"gh": {Connected: true, Tools: 3}}
}

func (f *fakeRuntime) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loaded[len(f.loaded)-1]
}

func setup(t *testing.T) (*App, *fakeRuntime, *repomcpserver.Repository) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	secrets, err := reposecret.New(filepath.Join(dir, "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secrets.Close() })
	rt := &fakeRuntime{}
	repo := repomcpserver.New(db, secrets)
	return New(repo, rt, nil), rt, repo
}

func TestServers(t *testing.T) {
	app, rt, repo := setup(t)
	ctx := context.Background()
	on := true

	if _, err := app.Create(ctx, Input{Name: "browser", Command: "x"}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("a built-in name = %v", err)
	}
	if _, err := app.Create(ctx, Input{Name: "gh"}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("stdio without a command = %v", err)
	}
	gh, err := app.Create(ctx, Input{Name: "gh", Command: "npx", Args: []string{"-y", "server"},
		Env: []Pair{{Name: "TOKEN", Value: "s3cret"}}, Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if gh.Status == nil || !gh.Status.Connected || len(gh.Env) != 1 || gh.Env[0] != "TOKEN" {
		t.Fatalf("created view = %+v", gh)
	}
	if _, err := app.Create(ctx, Input{Name: "gh", Command: "y"}); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("a duplicate name = %v", err)
	}
	remote, err := app.Create(ctx, Input{Name: "docs", Transport: "http", URL: "https://example.com/mcp",
		Headers: []Pair{{Name: "Authorization", Value: "Bearer k"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.last(); len(got) != 2 {
		t.Fatalf("runtime loaded %v", got)
	}

	// An edit that leaves a value empty keeps the stored one.
	readAt, _ := time.Parse(time.RFC3339Nano, gh.UpdatedAt)
	gh, err = app.Update(ctx, gh.ID, Input{Name: "gh", Command: "npx", Env: []Pair{{Name: "TOKEN"}, {Name: "MORE", Value: "m"}}}, readAt)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Get(ctx, gh.ID)
	if env := stored.Env(); env["TOKEN"] != "s3cret" || env["MORE"] != "m" {
		t.Fatalf("stored env = %v", env)
	}
	if _, err := app.Update(ctx, gh.ID, Input{Name: "gh", Command: "npx"}, readAt); apperror.As(err).StatusCode() != 409 {
		t.Fatalf("a stale edit = %v", err)
	}

	if _, err := app.SetEnabled(ctx, remote.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := rt.last(); len(got) != 1 || got[0] != "gh" {
		t.Fatalf("runtime after disabling docs = %v", got)
	}
	agents := app.ForAgents(ctx)
	if len(agents) != 1 || agents[0].Env["TOKEN"] != "s3cret" || agents[0].Transport != enum.MCPStdio {
		t.Fatalf("agents get %+v", agents)
	}

	if err := app.Delete(ctx, gh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, gh.ID); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("deleted server = %v", err)
	}
	if list, _ := app.List(ctx); len(list) != 1 || list[0].Name != "docs" || list[0].Headers[0] != "Authorization" {
		t.Fatalf("list = %+v", list)
	}
}

// Watch picks up a change made by another process.
func TestWatch(t *testing.T) {
	app, rt, repo := setup(t)
	ctx := t.Context()
	go app.Watch(ctx, 20*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	s, _ := aggregate.NewMCPServer("", "cli", time.Now())
	_ = s.SetStdio("x", nil, nil)
	if err := repo.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rt.mu.Lock()
		n := len(rt.loaded)
		rt.mu.Unlock()
		if n > 0 && rt.last()[0] == "cli" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("watch did not reload")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
