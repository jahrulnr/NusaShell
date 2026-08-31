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
		MethodToolContracts,
		MethodProvidersList, MethodProvidersSave, MethodProvidersDelete, MethodProvidersTest,
		MethodProvidersImport, MethodModelsList,
		MethodSkillsList, MethodSkillsRead, MethodSkillsSave, MethodSkillsDelete,
		MethodPluginList, MethodPluginSave, MethodPluginDelete, MethodPluginTest,
		MethodPluginStop, MethodPluginToolsList, MethodPluginUninstall,
		MethodMemoryList, MethodMemorySave, MethodMemorySearch, MethodMemoryDelete,
		MethodTodosGet, MethodTodosDelete,
		MethodDocsList, MethodDocsSearch, MethodDocsRead,
		MethodLogsList, MethodLogsClear,
		MethodSettingsGet, MethodSettingsSet,
		MethodCIRunsStart, MethodCIRunsList, MethodCIRunsGet, MethodCIRunsCancel, MethodCIRunsRetry,
		MethodCIJobsGet, MethodCIJobsLogs, MethodCIJobsCancel, MethodCIArtifactsList,
		MethodCIRunnersList, MethodCICacheList, MethodCICacheClear,
		MethodAutomationList, MethodAutomationGet, MethodAutomationSave, MethodAutomationDelete,
		MethodAutomationEnable, MethodAutomationDisable, MethodAutomationRun, MethodAutomationValidate,
		MethodAutomationEvents, MethodAutomationIngest, MethodAutomationDependents,
		MethodAutomationSchedules, MethodAutomationCapabilities, MethodAutomationSetDisabled,
		MethodAcpAgentsList, MethodAcpAgentsSave, MethodAcpAgentsDelete,
		MethodAcpAgentsProbe, MethodAcpAgentsAuthenticate, MethodAcpAgentsRefreshCatalog,
		MethodAcpRunsList, MethodAcpRunsGet, MethodAcpRunsSteer, MethodAcpRunsStop,
		MethodAcpRunsWait, MethodAcpRunsPromote, MethodAcpRunsSetMode,
		MethodAcpPermissionDecide,
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
		EventTurnStarted, EventToolStarted, EventToolCompleted,
		EventTurnDone, EventTurnError, EventCompacting, EventCompacted, EventCompactionFailed, EventSteerQueued, EventSteerApplied,
		EventSteerCancelled, EventProviderRetry, EventLogAppend, EventTodoUpdated,
		EventCIRunCreated, EventCIRunStarted, EventCIRunCompleted, EventCIRunFailed,
		EventCIRunCancelled, EventCIRunWaiting, EventCIRunBlocked,
		EventCIJobQueued, EventCIJobStarted, EventCIJobCompleted, EventCIJobFailed,
		EventCIJobCancelled, EventCIJobSkipped,
		EventCIStepStarted, EventCIStepOutput, EventCIStepCompleted, EventCIStepFailed,
		EventAutomationEvent,
		EventAcpRunStarted, EventAcpRunUpdated, EventAcpRunDone,
		EventAcpPermissionRequested, EventAcpPermissionDecided, EventAcpSessionModeChanged,
		EventLearningReviewStarted, EventLearningReviewDone,
		EventSettingsApplied, EventSettingsRejected,
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
		Type:    EventTurnStarted,
		Payload: json.RawMessage(`{"run_id":"run_1","conversation_id":"conv_1","message_id":"msg_1","round":1}`),
	})
}

func TestTurnErrorEventCarriesMessageID(t *testing.T) {
	payload, err := json.Marshal(TurnErrorEvent{
		RunID: "run_1", ConversationID: "conv_1", MessageID: "msg_1", Message: "provider failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["message_id"] != "msg_1" {
		t.Fatalf("message_id = %q, want msg_1", got["message_id"])
	}
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
			ID: "tc_1", Name: "docs", Args: json.RawMessage(`{"op":"search","query":"mcp"}`),
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
	assertGolden(t, "acp-agent.json", AcpAgentDTO{
		ID: "acp_1", Name: "Cursor", Command: "cursor", Args: []string{"agent", "acp"},
		EnvKeys: []string{"CURSOR_API_KEY"}, Enabled: true,
		PreferredModeID:    "plan",
		CachedCapabilities: AcpCapabilitiesDTO{HasModes: true, HasFS: true},
		CachedModes:        []AcpModeDTO{{ID: "plan", Name: "Plan", RiskTier: "read_only"}},
		CachedModels:       []AcpModelDTO{{ID: "auto", Name: "auto", Tier: "unclassified"}},
		UpdatedAt:          "2026-08-17T00:00:00Z",
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

	b, _ = json.Marshal(ToolCompletedEvent{
		RunID: "r", ConversationID: "c", ToolCallID: "t", Name: "generate_image", Args: json.RawMessage(`{"prompt":"x"}`), Status: "ok",
		Presentation: &ToolPresentationDTO{
			Variant: "media", Action: "Image generated", Request: "generate_image(...)",
			Result: ToolPresentationResultDTO{Format: "status", Summary: "1 image"},
		},
		Attachments: []AttachmentDTO{{Type: "image", Name: "gen.png", FilePath: "/tmp/gen.png"}},
	})
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tool_call_id", "args", "attachments", "presentation"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q in ToolCompletedEvent JSON", k)
		}
	}
	if got := m["presentation"].(map[string]any)["variant"]; got != "media" {
		t.Errorf("presentation.variant = %v, want media", got)
	}

	b, _ = json.Marshal(RoundDeltaFrame{
		Seq: 1, Kind: RoundDeltaTool, ToolCallID: "t", Name: "exec", Args: json.RawMessage(`{"command":"printf hi"}`), Text: "line1\n",
		Presentation: &ToolPresentationDTO{Variant: "terminal", Action: "Running command", Result: ToolPresentationResultDTO{Format: "terminal"}},
	})
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"seq", "kind", "tool_call_id", "name", "args", "text", "presentation"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q in RoundDeltaFrame JSON", k)
		}
	}
}

func TestToolCallPresentationRoundTripKeepsRawOutputSeparate(t *testing.T) {
	in := ToolCallDTO{
		ID: "tc_1", Name: "file_list", Args: json.RawMessage(`{"path":"/workspace"}`),
		Status: "ok", Output: "---\ncount: 2\n---\n-rw file-a\n-rw file-b",
		Presentation: &ToolPresentationDTO{
			Variant: "file-list", Action: "Files listed", Request: "file_list({\n  \"path\": \"/workspace\"\n})",
			Result: ToolPresentationResultDTO{
				Format: "list", Summary: "2 entries", Meta: map[string]any{"count": 2},
				Items: []map[string]any{{"name": "file-a"}, {"name": "file-b"}},
			},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ToolCallDTO
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Output != in.Output {
		t.Fatalf("raw output changed: got %q, want %q", out.Output, in.Output)
	}
	if out.Presentation == nil || out.Presentation.Result.Summary != "2 entries" {
		t.Fatalf("presentation was not round-tripped: %+v", out.Presentation)
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
