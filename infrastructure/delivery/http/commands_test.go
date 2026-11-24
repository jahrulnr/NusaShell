package http

import (
	"context"
	"errors"
	"testing"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// fakeCommands stands in for the application's command catalog and executor.
type fakeCommands struct {
	rows   []appconversation.Command
	result appconversation.CommandResult
	err    error

	asked   string // conversationId the catalog was read for
	ranFor  string
	lastIn  appconversation.CommandInput
	ranOnce bool
}

func (f *fakeCommands) Commands(_ context.Context, convID string) ([]appconversation.Command, error) {
	f.asked = convID
	return f.rows, f.err
}

func (f *fakeCommands) RunCommand(_ context.Context, convID string, in appconversation.CommandInput) (appconversation.CommandResult, error) {
	f.ranFor, f.lastIn, f.ranOnce = convID, in, true
	return f.result, f.err
}

// The catalog answers the native rows with every field the composer renders,
// and a room's id reaches the application.
func TestCommandCatalog(t *testing.T) {
	fc := &fakeCommands{rows: []appconversation.Command{
		{Name: "compact", Description: "Summarize older history into a checkpoint", Source: "native"},
		{Name: "review", Description: "Review the working tree", Hint: "/review [path]", Source: "agent", Enabled: true},
	}}
	h := newTestHandler(t, Deps{Commands: fc})

	rec := do(t, h, "GET", "/api/commands", "", "")
	if rec.Code != 200 || fc.asked != "" {
		t.Fatalf("fresh composer: %d asked=%q", rec.Code, fc.asked)
	}
	doc := decodeBody(t, rec)
	rows := doc["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	first := rows[0].(map[string]any)
	if first["name"] != "compact" || first["source"] != "native" || first["enabled"] != false {
		t.Fatalf("native row = %v", first)
	}
	second := rows[1].(map[string]any)
	if second["name"] != "review" || second["hint"] != "/review [path]" || second["enabled"] != true {
		t.Fatalf("agent row = %v", second)
	}

	rec = do(t, h, "GET", "/api/commands?conversationId=c_1", "", "")
	if rec.Code != 200 || fc.asked != "c_1" {
		t.Fatalf("room catalog: %d asked=%q", rec.Code, fc.asked)
	}
}

// Running a command answers the command's name and the room it left the caller
// on, in the detail projection the composer reloads.
func TestRunCommandAnswersProjection(t *testing.T) {
	fc := &fakeCommands{result: appconversation.CommandResult{
		Command:      "fork",
		Conversation: drepo.ConversationInfo{ID: "c_fork", Title: "origin (fork)", MessageCount: 3},
	}}
	convs := &fakeConversations{infos: []drepo.ConversationInfo{{ID: "c_fork", Title: "origin (fork)", MessageCount: 3}}}
	h := newTestHandler(t, Deps{Commands: fc, Conversations: convs})

	rec := doBody(t, h, "POST", "/api/conversations/c_1/commands", "application/json", `{"name":"fork","args":"","confirm":true}`)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if fc.ranFor != "c_1" || fc.lastIn.Name != "fork" || !fc.lastIn.Confirm {
		t.Fatalf("input = %q %+v", fc.ranFor, fc.lastIn)
	}
	doc := decodeBody(t, rec)
	if doc["command"] != "fork" {
		t.Fatalf("command = %v", doc["command"])
	}
	room, ok := doc["conversation"].(map[string]any)
	if !ok || room["id"] != "c_fork" || room["title"] != "origin (fork)" {
		t.Fatalf("conversation = %v", doc["conversation"])
	}
	if _, ok := room["canCompact"]; !ok {
		t.Fatalf("detail projection lacks canCompact: %v", room)
	}
}

// A refusal keeps the contract's stable code: a missing confirmation is not an
// invalid argument, and an empty thread has its own nothing-to code.
func TestRunCommandRefusalsKeepTheirCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status float64
		code   string
	}{
		{"busy", apperror.NewCoded(409, "busy", "wait for the turn to finish", nil), 409, "busy"},
		{"confirm", apperror.NewCoded(409, "confirmation_required", "nothing is cleared without confirm", nil), 409, "confirmation_required"},
		{"unavailable", apperror.NewCoded(409, "unavailable", "the agent manages its own context", nil), 409, "unavailable"},
		{"empty", apperror.NewCoded(400, "nothing_to_clear", "this thread is empty", nil), 400, "nothing_to_clear"},
		{"unknown", apperror.New(400, "unknown command", errors.New("no name")), 400, "invalid_argument"},
	} {
		fc := &fakeCommands{err: tc.err}
		h := newTestHandler(t, Deps{Commands: fc, Conversations: &fakeConversations{}})
		rec := doBody(t, h, "POST", "/api/conversations/c_1/commands", "application/json", `{"name":"clear","args":"","confirm":false}`)
		if float64(rec.Code) != tc.status {
			t.Fatalf("%s: want %v, got %d %s", tc.name, tc.status, rec.Code, rec.Body.String())
		}
		if got := decodeBody(t, rec)["code"]; got != tc.code {
			t.Fatalf("%s: code = %v, want %v", tc.name, got, tc.code)
		}
	}
}

// The replaced routes are gone: history operations are commands now.
func TestReplacedRoutesAreGone(t *testing.T) {
	h := newTestHandler(t, Deps{
		Commands:           &fakeCommands{},
		Conversations:      &fakeConversations{infos: []drepo.ConversationInfo{{ID: "c_1"}}},
		ConversationWrites: &fakeConvWrites{},
	})
	rec := doBody(t, h, "POST", "/api/conversations/c_1/compactions", "application/json", `{}`)
	if rec.Code != 404 {
		t.Fatalf("compactions route: want 404, got %d", rec.Code)
	}
	rec = do(t, h, "GET", "/api/conversations/c_1/commands", "", "")
	if rec.Code != 404 {
		t.Fatalf("per-room command list: want 404, got %d", rec.Code)
	}
}
