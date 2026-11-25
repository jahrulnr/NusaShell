package toolexec

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// runSession calls exec_session the way a model does.
func runSession(t *testing.T, tool dservice.Tool, convID, input string) (dservice.ToolResult, error) {
	t.Helper()
	call, err := entity.NewToolCall("call_1", "exec_session", input, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	ctx := dservice.WithConversationID(t.Context(), convID)
	return tool.Execute(ctx, call, nil)
}

// The tool surface is the opt-in: `wake: true` in the call is what makes the exit
// a record, and the default call makes none.
func TestExecSessionWakeOptsTheSessionIn(t *testing.T) {
	store := NewSessionStore(nil)
	var mu sync.Mutex
	var got []dservice.Completion
	seen := make(chan struct{}, 4)
	store.OnExit(func(_ string, c dservice.Completion) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
		seen <- struct{}{}
	})
	tool, err := NewExecSessionTool(store)
	if err != nil {
		t.Fatal(err)
	}
	defer store.TerminateConversation("c_tool")

	// A clean exit through the tool.
	if _, err := runSession(t, tool, "c_tool", `{"command":"echo from-tool","wake":true}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the opted-in session's exit was never reported")
	}
	mu.Lock()
	clean := got[len(got)-1]
	mu.Unlock()
	// The call already drained the output — the completion carries what is left
	// unread, so a read result is never duplicated into the record.
	if clean.Status != "exited" || !strings.Contains(clean.Detail, "exit 0") {
		t.Fatalf("clean exit = %+v", clean)
	}

	// A failing exit through the tool: the record says failed, with the code.
	if _, err := runSession(t, tool, "c_tool", `{"command":"exit 7","wake":true}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the failing session's exit was never reported")
	}
	mu.Lock()
	failed := got[len(got)-1]
	mu.Unlock()
	if failed.Status != "failed" || !strings.Contains(failed.Detail, "exit 7") {
		t.Fatalf("failed exit = %+v", failed)
	}

	// The same call without `wake` reports nothing at all.
	if _, err := runSession(t, tool, "c_tool", `{"command":"echo quiet"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
		t.Fatalf("a call without wake reported: %+v", got)
	case <-time.After(400 * time.Millisecond):
	}
}
