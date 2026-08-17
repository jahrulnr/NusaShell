package acpruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	fakeBin = tmp.Name()
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

func TestPermissionPromptThenAllow(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	gotPerm := make(chan domain.AcpPermissionRequest, 1)
	rt.SetCallbacks(nil, nil, func(_ *domain.AcpRun, req domain.AcpPermissionRequest) {
		gotPerm <- req
	}, nil)
	run, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent: testAgent(ws), Prompt: "NEED_PERMISSION edit", Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	var req domain.AcpPermissionRequest
	select {
	case req = <-gotPerm:
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for permission prompt")
	}
	if err := rt.DecidePermission(run.ID, req.ID, "allow-once", domain.PermissionAllowOnce); err != nil {
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
