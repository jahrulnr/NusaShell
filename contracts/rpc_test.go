package contracts

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden fixtures")

const goldenDir = "testdata/golden"

func goldenPath(name string) string {
	return filepath.Join(goldenDir, name)
}

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	b = append(b, '\n')
	path := goldenPath(name)
	if *update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update): %v", path, err)
	}
	if string(b) != string(want) {
		t.Errorf("golden mismatch %s:\n got: %s\nwant: %s", name, b, want)
	}
}

func TestRosterUniqueness(t *testing.T) {
	methods := []string{
		MethodAppInfo,
		MethodConversationsList, MethodConversationsCreate, MethodConversationsGet,
		MethodConversationsRename, MethodConversationsDelete, MethodConversationsPickWorkspace,
		MethodConversationsChunk, MethodTurnsStart, MethodTurnsStop,
		MethodTurnsRetry, MethodTurnsSteer, MethodTurnsCancelSteer, MethodTurnsActive,
		MethodProvidersList, MethodProvidersSave, MethodProvidersDelete, MethodProvidersTest,
		MethodProvidersImport, MethodModelsList,
		MethodSkillsList, MethodSkillsRead, MethodSkillsSave, MethodSkillsDelete, MethodSkillsRun,
		MethodMCPServersList, MethodMCPServersSave, MethodMCPServersDelete, MethodMCPServersTest,
		MethodMCPToolsList,
		MethodMemoryList, MethodMemorySave, MethodMemorySearch, MethodMemoryDelete,
		MethodTodosGet, MethodTodosDelete,
		MethodDocsList, MethodDocsSearch, MethodDocsRead,
		MethodLogsList, MethodLogsClear,
		MethodSettingsGet, MethodSettingsSet,
	}
	seen := map[string]bool{}
	for _, m := range methods {
		if m == "" {
			t.Error("empty method constant in roster")
		}
		if seen[m] {
			t.Errorf("duplicate method in roster: %s", m)
		}
		seen[m] = true
	}

	events := []string{
		EventTurnStarted, EventMessageDelta, EventReasoningDelta, EventToolStarted, EventToolCompleted,
		EventTurnDone, EventTurnError, EventCompacted, EventSteerQueued, EventSteerApplied,
		EventSteerCancelled, EventProviderRetry, EventLogAppend, EventTodoUpdated,
	}
	seenEv := map[string]bool{}
	for _, e := range events {
		if e == "" {
			t.Error("empty event constant in roster")
		}
		if seenEv[e] {
			t.Errorf("duplicate event in roster: %s", e)
		}
		seenEv[e] = true
	}
}

func TestGoldenEnvelopes(t *testing.T) {
	assertGolden(t, "request.json", Request{
		Method:  MethodConversationsCreate,
		Payload: json.RawMessage(`{"title":"hello"}`),
	})
	assertGolden(t, "response-ok.json", Response{
		OK:     true,
		Result: json.RawMessage(`{"run_id":"run_1"}`),
	})
	assertGolden(t, "response-error.json", Response{
		OK: false,
		Error: &RPCError{
			Code:    CodeValidation,
			Message: "conversation id is required",
		},
	})
	assertGolden(t, "event.json", Event{
		Type:    EventMessageDelta,
		Payload: json.RawMessage(`{"run_id":"run_1","conversation_id":"conv_1","message_id":"msg_1","text":"hi"}`),
	})
}

func TestGoldenDTOs(t *testing.T) {
	assertGolden(t, "conversation.json", ConversationDTO{
		ID: "conv_1", Title: "Hello", CreatedAt: "2026-08-12T00:00:00Z",
		UpdatedAt: "2026-08-12T00:01:00Z", MessageCount: 3, Model: "claude-3-5-haiku", Workspace: "/home/nusa/project",
	})
	assertGolden(t, "message.json", MessageDTO{
		ID: "msg_1", Role: "assistant", Content: "Hello!",
		Model: "claude-3-5-haiku", CreatedAt: "2026-08-12T00:00:00Z",
		Usage: &UsageDTO{InputTokens: 120, OutputTokens: 40, CacheRead: 80},
		ToolCalls: []ToolCallDTO{{
			ID: "tc_1", Name: "docs_search", Args: json.RawMessage(`{"query":"mcp"}`),
			Status: "ok", Output: "docs/mcp.md",
		}},
		Attachments: []AttachmentDTO{{
			Type: "image", Name: "diagram.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo=",
		}},
	})
	assertGolden(t, "provider.json", ProviderDTO{
		ID: "prov_1", Kind: "chat", Name: "Local", BaseURL: "http://127.0.0.1:11434/v1",
		Enabled: true, Configured: true, HasAPIKey: false,
		Models: []ModelDTO{{ID: "llama3.1", ProviderID: "prov_1", ProviderName: "Local", Context: 128000}},
	})
	assertGolden(t, "mcp-server.json", MCPServerDTO{
		ID: "mcp_1", Name: "files", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"},
		Enabled: true, Status: "connected",
		Tools: []MCPToolDTO{{Name: "read_file", Description: "Read a file"}},
	})
	assertGolden(t, "log-entry.json", LogEntryDTO{
		ID: "log_1", Time: "2026-08-12T00:00:00Z", Level: "info", Source: "agent", Message: "turn started",
	})
	assertGolden(t, "turn-started-event.json", TurnStartedEvent{
		RunID: "run_1", ConversationID: "conv_1", MessageID: "msg_1",
	})
}

func TestDecodePayload(t *testing.T) {
	var req ConversationCreateRequest
	if err := DecodePayload(json.RawMessage(`{"title":"x"}`), &req); err != nil {
		t.Fatalf("decode valid payload: %v", err)
	}
	if req.Title != "x" {
		t.Errorf("title = %q, want %q", req.Title, "x")
	}

	var bad ConversationCreateRequest
	rpcErr := DecodePayload(json.RawMessage(`{"title": 42}`), &bad)
	if rpcErr == nil {
		t.Fatal("want *RPCError, got nil")
	}
	if rpcErr.Code != CodeValidation {
		t.Errorf("code = %q, want VALIDATION_ERROR", rpcErr.Code)
	}

	var empty ConversationCreateRequest
	if err := DecodePayload(nil, &empty); err != nil {
		t.Fatalf("empty payload should decode to zero value: %v", err)
	}
}

func TestResponseHelpers(t *testing.T) {
	ok := OKResult(map[string]string{"a": "b"})
	if !ok.OK || ok.Error != nil {
		t.Fatalf("OKResult malformed: %+v", ok)
	}
	var m map[string]string
	if err := json.Unmarshal(ok.Result, &m); err != nil || m["a"] != "b" {
		t.Fatalf("OKResult payload = %s", ok.Result)
	}
	err := ErrResult(CodeNotFound, "nope")
	if err.OK || err.Error == nil || err.Error.Code != CodeNotFound {
		t.Fatalf("ErrResult malformed: %+v", err)
	}
}

func TestEventFieldNames(t *testing.T) {
	// Pin the exact on-wire field names the frontend relies on.
	var m map[string]any
	b, _ := json.Marshal(TurnDoneEvent{RunID: "r", ConversationID: "c", MessageID: "m"})
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"run_id", "conversation_id", "message_id"}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q in TurnDoneEvent JSON", k)
		}
	}
}

func TestMessageDTORoundTrip(t *testing.T) {
	in := MessageDTO{
		ID: "m1", Role: "assistant", Content: "hi", CreatedAt: "t",
		Usage: &UsageDTO{InputTokens: 1}, ToolCalls: []ToolCallDTO{{ID: "t1", Name: "x"}},
		Attachments: []AttachmentDTO{{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "hello"}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out MessageDTO
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip mismatch:\n in: %+v\nout: %+v", in, out)
	}
}
