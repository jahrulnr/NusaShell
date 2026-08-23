package acpclient

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
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
	// -buildvcs=false: on network shares git refuses VCS stamping (dubious
	// ownership), which would fail the whole package's tests.
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", fakeBin, filepath.Join(root, "testdata", "fakeacp"))
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

type recHandler struct {
	mu      sync.Mutex
	updates []SessionUpdateParams
	perms   int
}

func (h *recHandler) RequestPermission(ctx context.Context, params RequestPermissionParams) (RequestPermissionResult, error) {
	h.mu.Lock()
	h.perms++
	h.mu.Unlock()
	return RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "allow-once"}}, nil
}
func (h *recHandler) ReadTextFile(ctx context.Context, params ReadTextFileParams) (ReadTextFileResult, error) {
	return ReadTextFileResult{Content: "ok"}, nil
}
func (h *recHandler) WriteTextFile(ctx context.Context, params WriteTextFileParams) error { return nil }
func (h *recHandler) SessionUpdate(params SessionUpdateParams) {
	h.mu.Lock()
	h.updates = append(h.updates, params)
	h.mu.Unlock()
}

func TestDialInitializePrompt(t *testing.T) {
	h := &recHandler{}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := Dial(ctx, fakeBin, nil, os.Environ(), t.TempDir(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	init, err := conn.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if init.AgentInfo.Name != "fakeacp" {
		t.Fatalf("agent %q", init.AgentInfo.Name)
	}
	sess, err := conn.NewSession(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if sess.SessionID == "" || sess.Modes == nil || sess.Modes.CurrentModeID != "plan" {
		t.Fatalf("%+v", sess)
	}
	res, err := conn.Prompt(ctx, sess.SessionID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("stop %q", res.StopReason)
	}
	h.mu.Lock()
	n := len(h.updates)
	h.mu.Unlock()
	if n == 0 {
		t.Fatal("expected session/update")
	}
}

func TestAuthenticateGate(t *testing.T) {
	h := &recHandler{}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	env := append(os.Environ(), "FAKEACP_AUTH=1")
	conn, err := Dial(ctx, fakeBin, nil, env, t.TempDir(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	init, err := conn.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(init.AuthMethods) == 0 {
		t.Fatal("expected auth methods")
	}
	if _, err := conn.NewSession(ctx, t.TempDir()); err == nil {
		t.Fatal("session/new should fail before authenticate")
	}
	if err := conn.Authenticate(ctx, "cursor_login"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.NewSession(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestPromptPermissionAndCancel(t *testing.T) {
	h := &recHandler{}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := Dial(ctx, fakeBin, nil, os.Environ(), t.TempDir(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sess, err := conn.NewSession(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Prompt(ctx, sess.SessionID, "NEED_PERMISSION please"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	perms := h.perms
	h.mu.Unlock()
	if perms == 0 {
		t.Fatal("expected permission request")
	}

	done := make(chan PromptResult, 1)
	go func() {
		res, _ := conn.Prompt(ctx, sess.SessionID, "SLOW task")
		done <- res
	}()
	time.Sleep(50 * time.Millisecond)
	_ = conn.Cancel(sess.SessionID)
	select {
	case res := <-done:
		if res.StopReason != "cancelled" && res.StopReason != "end_turn" {
			t.Fatalf("stop %q", res.StopReason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not unblock prompt")
	}
}

func TestRPCErrorCarriesJSONRPCCode(t *testing.T) {
	h := &recHandler{}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	env := append(os.Environ(), "FAKEACP_AUTH=1")
	conn, err := Dial(ctx, fakeBin, nil, env, t.TempDir(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = conn.NewSession(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected auth-required error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected typed RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != -32000 {
		t.Fatalf("code = %d, want -32000", rpcErr.Code)
	}
	if rpcErr.Message != "authentication required" {
		t.Fatalf("message = %q", rpcErr.Message)
	}
	// Wire text stays backward compatible for callers matching on message.
	if err.Error() != "acp authentication required" {
		t.Fatalf("Error() = %q", err.Error())
	}
}
