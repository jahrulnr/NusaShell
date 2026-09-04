package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/domain/turndiff"
)

type turnDiffToolbox struct{}

func (turnDiffToolbox) ListTools() []ToolInfo { return nil }

func (turnDiffToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	var args map[string]any
	_ = json.Unmarshal(argsJSON, &args)
	switch name {
	case "file_write":
		path, _ := args["path"].(string)
		content, _ := args["content"].(string)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
		turndiff.Record(ctx, turndiff.AddFile(path, content, nil))
		return "ok", nil
	case "file_patch":
		path, _ := args["path"].(string)
		oldS, _ := args["old_string"].(string)
		newS, _ := args["new_string"].(string)
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		out := strings.Replace(string(raw), oldS, newS, 1)
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return "", err
		}
		turndiff.Record(ctx, turndiff.UpdateFile(path, string(raw), out, nil, nil))
		return "ok", nil
	case "exec":
		return "hello-exec\n", nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func waitTurnDiff(t *testing.T, events <-chan contracts.Event) contracts.TurnDiffEvent {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev := <-events:
			if ev.Type != contracts.EventTurnDiff {
				continue
			}
			var payload contracts.TurnDiffEvent
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("payload: %v", err)
			}
			return payload
		case <-deadline.C:
			t.Fatal("timed out waiting for agent.turn.diff")
		}
	}
}

func TestRunOneToolFileWriteEmitsTurnDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foo.txt")
	bus := NewBus()
	_, events, unsub := bus.Subscribe()
	defer unsub()
	app := &App{Bus: bus, Toolbox: turnDiffToolbox{}}
	run := &TurnRun{
		ID:             "run1",
		ConversationID: "conv1",
		Workspace:      dir,
		Ctx:            context.Background(),
		TurnDiff:       turndiff.New(turndiff.WithDisplayRoot(dir)),
	}
	res := app.runOneTool(run, "m1", domain.ToolCall{
		ID:   "tc1",
		Name: "file_write",
		Args: `{"path":"` + jsonEscape(path) + `","content":"hello\n"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.status != domain.ToolOK {
		t.Fatalf("status = %v output=%q", res.status, res.output)
	}
	payload := waitTurnDiff(t, events)
	if payload.RunID != "run1" || payload.ConversationID != "conv1" {
		t.Fatalf("ids = %+v", payload)
	}
	if !strings.Contains(payload.UnifiedDiff, "diff --git a/foo.txt b/foo.txt") {
		t.Fatalf("unified_diff = %q", payload.UnifiedDiff)
	}
	if !strings.Contains(payload.UnifiedDiff, "+hello") {
		t.Fatalf("unified_diff missing content: %q", payload.UnifiedDiff)
	}
}

func TestRunOneToolFilePatchEmitsTurnDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	bus := NewBus()
	app := &App{Bus: bus, Toolbox: turnDiffToolbox{}}
	run := &TurnRun{
		ID:             "run1",
		ConversationID: "conv1",
		Workspace:      dir,
		Ctx:            context.Background(),
		TurnDiff:       turndiff.New(turndiff.WithDisplayRoot(dir)),
	}
	write := app.runOneTool(run, "m1", domain.ToolCall{
		ID: "tc1", Name: "file_write", Args: `{"path":"` + jsonEscape(path) + `","content":"line1\n"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if write.status != domain.ToolOK {
		t.Fatalf("write status = %v output=%q", write.status, write.output)
	}
	_, events, unsub := bus.Subscribe()
	defer unsub()
	patch := app.runOneTool(run, "m1", domain.ToolCall{
		ID: "tc2", Name: "file_patch", Args: `{"path":"` + jsonEscape(path) + `","old_string":"line1\n","new_string":"line2\n"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if patch.status != domain.ToolOK {
		t.Fatalf("patch status = %v output=%q", patch.status, patch.output)
	}
	payload := waitTurnDiff(t, events)
	if !strings.Contains(payload.UnifiedDiff, "+line2") {
		t.Fatalf("unified_diff = %q", payload.UnifiedDiff)
	}
}

func TestRunOneToolExecDoesNotCreateJournal(t *testing.T) {
	dataDir := t.TempDir()
	ws := filepath.Join(dataDir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	app := &App{DataDir: dataDir, Bus: NewBus(), Toolbox: turnDiffToolbox{}}
	run := &TurnRun{
		ID:             "run1",
		ConversationID: "conv1",
		Workspace:      ws,
		Ctx:            context.Background(),
		TurnDiff:       turndiff.New(turndiff.WithDisplayRoot(ws)),
	}
	res := app.runOneTool(run, "m1", domain.ToolCall{
		ID:   "tc1",
		Name: "exec",
		Args: `{"command":"echo hello-exec"}`,
	}, ModelCapabilities{}, domain.Settings{}, 1)
	if res.status != domain.ToolOK {
		t.Fatalf("exec status = %v output=%q", res.status, res.output)
	}
	matches, err := filepath.Glob(filepath.Join(dataDir, "conversations", "*.journal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("exec created journal sidecars: %v", matches)
	}
	run.turnDiffMu.Lock()
	_, ok := run.TurnDiff.UnifiedDiff()
	run.turnDiffMu.Unlock()
	if ok {
		t.Fatal("exec must not produce a turn diff")
	}
}

func jsonEscape(p string) string {
	b, _ := json.Marshal(p)
	s := string(b)
	return s[1 : len(s)-1]
}
