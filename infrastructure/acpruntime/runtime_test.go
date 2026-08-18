package acpruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

var fakeBin string

func TestMain(m *testing.M) {
	root, err := findModRoot()
	if err != nil {
		os.Exit(1)
	}
	tmp, err := os.CreateTemp("", "fakeacp-*")
	if err != nil {
		os.Exit(1)
	}
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	fakeBin = tmp.Name()
	if runtime.GOOS == "windows" {
		fakeBin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", fakeBin, filepath.Join(root, "testdata", "fakeacp"))
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.Remove(fakeBin)
	os.Exit(code)
}

func findModRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func testAgent(workspace string) *domain.AcpAgent {
	return &domain.AcpAgent{
		ID:               "acp_test",
		Name:             "Fake ACP",
		Command:          fakeBin,
		Enabled:          true,
		DefaultWorkspace: workspace,
	}
}

func TestSpawnWaitCompletes(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	run, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent:     testAgent(ws),
		Prompt:    "hello from parent",
		Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Workspace != ws {
		t.Fatalf("workspace = %q, want %q", run.Workspace, ws)
	}
	if run.CurrentModeID != "plan" {
		t.Fatalf("default mode = %q, want plan (strictest)", run.CurrentModeID)
	}
	finished, err := rt.Wait(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.AcpRunCompleted {
		t.Fatalf("status = %s error=%s", finished.Status, finished.Error)
	}
	found := false
	for _, c := range finished.Transcript {
		if strings.Contains(c.Text, "hello from parent") {
			found = true
		}
	}
	if !found {
		t.Fatalf("transcript missing prompt echo: %+v", finished.Transcript)
	}
}

func TestMultiSpawnSharesProcess(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agent := testAgent(ws)
	a, err := rt.Spawn(ctx, application.AcpSpawnRequest{Agent: agent, Prompt: "one", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.Spawn(ctx, application.AcpSpawnRequest{Agent: agent, Prompt: "two", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionID == b.SessionID {
		t.Fatal("expected distinct ACP session ids")
	}
	if _, err := rt.Wait(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	n := len(rt.conns)
	rt.mu.Unlock()
	if n != 1 {
		t.Fatalf("pooled connections = %d, want 1 for same workspace", n)
	}
}

func TestDynamicWorkspaceKeepsBoundRun(t *testing.T) {
	rt := New()
	defer rt.Close()
	first := t.TempDir()
	second := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agent := testAgent(first)
	slow, err := rt.Spawn(ctx, application.AcpSpawnRequest{Agent: agent, Prompt: "SLOW keep-cwd", Workspace: first})
	if err != nil {
		t.Fatal(err)
	}
	other, err := rt.Spawn(ctx, application.AcpSpawnRequest{Agent: agent, Prompt: "other ws", Workspace: second})
	if err != nil {
		t.Fatal(err)
	}
	if slow.Workspace != first || other.Workspace != second {
		t.Fatalf("workspaces = %q %q", slow.Workspace, other.Workspace)
	}
	if _, err := rt.Wait(ctx, slow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
}

func TestStopCancelsSlowRun(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	run, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent: testAgent(ws), Prompt: "SLOW please", Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(run.ID); err != nil {
		t.Fatal(err)
	}
	finished, err := rt.Wait(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.AcpRunCancelled {
		t.Fatalf("status = %s, want cancelled", finished.Status)
	}
}

func TestPermissionAutoAllowedByOrchestrator(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	run, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent: testAgent(ws), Prompt: "NEED_PERMISSION edit", Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := rt.Wait(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.AcpRunCompleted {
		t.Fatalf("status = %s error=%s", finished.Status, finished.Error)
	}
}

func TestContainedPathRejectsSlashRooted(t *testing.T) {
	ws := filepath.Join(string(filepath.Separator), "proj")
	_, err := containedPath(ws, filepath.Join(string(filepath.Separator), "etc", "passwd"))
	if err == nil {
		t.Fatal("slash-rooted path outside workspace must be rejected")
	}
	got, err := containedPath(ws, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "main.go")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestProbeDiscoversAuthMethods(t *testing.T) {
	rt := New()
	defer rt.Close()
	agent := testAgent(t.TempDir())
	agent.Env = map[string]string{"FAKEACP_AUTH": "1"}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	updated, err := rt.Probe(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.CachedAuthMethods) == 0 || updated.CachedAuthMethods[0].ID != "cursor_login" {
		t.Fatalf("auth methods = %+v", updated.CachedAuthMethods)
	}
}

func TestSpawnFailsWithoutAuthenticate(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	agent := testAgent(ws)
	agent.Env = map[string]string{"FAKEACP_AUTH": "1"}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent:     agent,
		Prompt:    "hello",
		Workspace: ws,
	})
	if err == nil {
		t.Fatal("expected spawn to fail without authenticate")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("error = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "cursor_login") {
		t.Fatalf("error should list auth method ids: %q", err.Error())
	}
}

func TestRefreshCatalogFailsWithoutAuthenticate(t *testing.T) {
	rt := New()
	defer rt.Close()
	agent := testAgent(t.TempDir())
	agent.Env = map[string]string{"FAKEACP_AUTH": "1"}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := rt.RefreshCatalog(ctx, agent)
	if err == nil {
		t.Fatal("expected refresh to fail without authenticate")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("error = %q", err.Error())
	}
}

// TestSpawnSucceedsAfterAuthenticateWithLazyAuth verifies that Spawn does not
// proactively re-call authenticate on the pooled connection. Instead it tries
// NewSession first and only calls Authenticate when the agent reports an auth
// error. This prevents agents that persist their own auth (e.g. Devin/Codex
// storing tokens in ~/.codex/auth.json) from re-triggering browser login on
// every spawn, probe, or catalog refresh.
func TestSpawnSucceedsAfterAuthenticateWithLazyAuth(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	agent := testAgent(ws)
	agent.Env = map[string]string{"FAKEACP_AUTH": "1"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Probe to discover auth methods.
	probed, err := rt.Probe(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(probed.CachedAuthMethods) == 0 {
		t.Fatal("expected auth methods")
	}

	// Authenticate once (withThrowaway — process is discarded).
	if err := rt.Authenticate(ctx, &probed, "cursor_login"); err != nil {
		t.Fatal(err)
	}

	// Spawn should succeed via lazy auth: NewSession fails → Authenticate → retry.
	run, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent:     &probed,
		Prompt:    "hello lazy",
		Workspace: ws,
	})
	if err != nil {
		t.Fatalf("spawn after authenticate: %v", err)
	}
	finished, err := rt.Wait(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.AcpRunCompleted {
		t.Fatalf("status = %s error=%s", finished.Status, finished.Error)
	}
}

// TestRefreshCatalogSucceedsAfterAuthenticateWithLazyAuth verifies that
// RefreshCatalog does not proactively re-authenticate. It tries NewSession
// first and only calls Authenticate on auth failure.
func TestRefreshCatalogSucceedsAfterAuthenticateWithLazyAuth(t *testing.T) {
	rt := New()
	defer rt.Close()
	agent := testAgent(t.TempDir())
	agent.Env = map[string]string{"FAKEACP_AUTH": "1"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	probed, err := rt.Probe(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Authenticate(ctx, &probed, "cursor_login"); err != nil {
		t.Fatal(err)
	}

	// RefreshCatalog should succeed via lazy auth.
	updated, err := rt.RefreshCatalog(ctx, &probed)
	if err != nil {
		t.Fatalf("refresh after authenticate: %v", err)
	}
	if len(updated.CachedModes) == 0 {
		t.Fatalf("expected cached modes after refresh: %+v", updated)
	}
}

// TestSpawnDoesNotProactivelyAuthenticateWithPersistedAuth simulates an agent
// that persists its own auth (like Devin/Codex storing tokens in
// ~/.codex/auth.json). FAKEACP_SOFT_AUTH advertises auth methods but
// session/new succeeds without authenticate. If authenticate IS called,
// session/new fails with "re-authentication triggered unnecessarily".
// This proves the runtime uses lazy auth (try NewSession first, only
// Authenticate on failure) instead of proactively re-authenticating.
func TestSpawnDoesNotProactivelyAuthenticateWithPersistedAuth(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	agent := testAgent(ws)
	agent.Env = map[string]string{"FAKEACP_SOFT_AUTH": "1"}
	agent.AuthMethodID = "cursor_login"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	run, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent:     agent,
		Prompt:    "hello persisted",
		Workspace: ws,
	})
	if err != nil {
		t.Fatalf("spawn with persisted auth: %v", err)
	}
	finished, err := rt.Wait(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.AcpRunCompleted {
		t.Fatalf("status = %s error=%s", finished.Status, finished.Error)
	}
}

// TestRefreshCatalogDoesNotProactivelyAuthenticateWithPersistedAuth is the
// catalog-refresh counterpart of TestSpawnDoesNotProactivelyAuthenticate.
func TestRefreshCatalogDoesNotProactivelyAuthenticateWithPersistedAuth(t *testing.T) {
	rt := New()
	defer rt.Close()
	agent := testAgent(t.TempDir())
	agent.Env = map[string]string{"FAKEACP_SOFT_AUTH": "1"}
	agent.AuthMethodID = "cursor_login"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	updated, err := rt.RefreshCatalog(ctx, agent)
	if err != nil {
		t.Fatalf("refresh with persisted auth: %v", err)
	}
	if len(updated.CachedModes) == 0 {
		t.Fatalf("expected cached modes after refresh: %+v", updated)
	}
}
