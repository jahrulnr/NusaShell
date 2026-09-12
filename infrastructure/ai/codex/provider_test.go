package codex

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestBuildRequestUsesConfiguredReasoningSummary(t *testing.T) {
	p := &Provider{}
	wire, _, err := p.buildRequest(t.Context(), &core.Request{
		Model:           "gpt-5.6-terra",
		Messages:        []core.Message{{Role: core.RoleUser, Blocks: []core.Block{core.TextBlock{Text: "hello"}}}},
		ProviderOptions: core.ProviderOptions{"reasoning_summary": "detailed"},
	})
	if err != nil {
		t.Fatalf("buildResponsesRequest: %v", err)
	}
	if wire.Reasoning == nil || wire.Reasoning.Summary != "detailed" {
		t.Fatalf("reasoning = %#v, want detailed summary", wire.Reasoning)
	}
}

func TestBuildRequestRejectsInvalidReasoningSummary(t *testing.T) {
	p := &Provider{}
	_, _, err := p.buildRequest(t.Context(), &core.Request{
		Model:           "gpt-5.6-terra",
		Messages:        []core.Message{{Role: core.RoleUser, Blocks: []core.Block{core.TextBlock{Text: "hello"}}}},
		ProviderOptions: core.ProviderOptions{"reasoning_summary": "verbose"},
	})
	if err == nil || !strings.Contains(err.Error(), "reasoning_summary") {
		t.Fatalf("error = %v, want reasoning_summary validation error", err)
	}
}

func TestBuildRequestOmitsReasoningSummaryWhenConfiguredNone(t *testing.T) {
	p := &Provider{}
	wire, _, err := p.buildRequest(t.Context(), &core.Request{
		Model:           "gpt-5.6-terra",
		Messages:        []core.Message{{Role: core.RoleUser, Blocks: []core.Block{core.TextBlock{Text: "hello"}}}},
		ProviderOptions: core.ProviderOptions{"reasoning_summary": "none"},
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if wire.Reasoning == nil || wire.Reasoning.Summary != "" {
		t.Fatalf("reasoning = %#v, want reasoning with omitted summary", wire.Reasoning)
	}
}

func TestRequestInputSplitsToolResultMediaIntoUserMessage(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	messages := []core.Message{
		core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "generate_image", Arguments: core.MustJSONRaw(map[string]any{"prompt": "x"})}),
		core.ToolResult("call_1",
			core.TextBlock{Text: "Image saved"},
			core.ImageBlock{Data: png, MIME: "image/png"}),
		core.User(core.TextBlock{Text: "lanjut"}),
	}
	_, input, err := requestInput(messages)
	if err != nil {
		t.Fatalf("requestInput: %v", err)
	}
	if len(input) != 4 {
		t.Fatalf("input items = %d, want 4: %+v", len(input), input)
	}
	if input[0].Type != ItemTypeFunctionCall || input[1].Type != ItemTypeFunctionCallOutput {
		t.Fatalf("first items = %s, %s; want function_call then function_call_output", input[0].Type, input[1].Type)
	}
	// function_call_output payload must stay plain text JSON ("Image saved").
	if got := string(input[1].Output); got != `"Image saved"` {
		t.Fatalf("tool output = %s, want JSON string of the text part", got)
	}
	// Media from the tool result is reinjected as a user message BEFORE the
	// next real user text, mirroring the OpenAI Responses deferredMedia
	// pattern — the tool output itself never carries an ImageBlock.
	mediaMsg := input[2]
	if mediaMsg.Type != ItemTypeMessage || mediaMsg.Role != RoleUser || len(mediaMsg.Content) != 1 {
		t.Fatalf("media reinjection = %+v", mediaMsg)
	}
	wantURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	if mediaMsg.Content[0].Type != ContentInputImage || mediaMsg.Content[0].ImageURL != wantURL {
		t.Fatalf("media item = %+v, want input_image %q", mediaMsg.Content[0], wantURL)
	}
	userMsg := input[3]
	if userMsg.Type != ItemTypeMessage || userMsg.Role != RoleUser ||
		len(userMsg.Content) != 1 || userMsg.Content[0].Text != "lanjut" {
		t.Fatalf("following user message = %+v", userMsg)
	}
}

func TestRequestInputToolResultWithoutTextKeepsEmptyOutput(t *testing.T) {
	messages := []core.Message{
		core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "read_media", Arguments: core.MustJSONRaw(map[string]any{"file_path": "/tmp/a.png"})}),
		core.ToolResult("call_1", core.ImageBlock{Data: []byte{1, 2, 3}, MIME: "image/png"}),
	}
	_, input, err := requestInput(messages)
	if err != nil {
		t.Fatalf("requestInput: %v", err)
	}
	if len(input) != 3 {
		t.Fatalf("input items = %d, want 3 (call + output + media user msg): %+v", len(input), input)
	}
	if got := string(input[1].Output); got != `""` {
		t.Fatalf("tool output = %s, want empty JSON string", got)
	}
}

func TestProviderStreamCompactionTriggerAndOutput(t *testing.T) {
	var requestBody ResponsesAPIRequest
	var authorization, originator, userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		authorization = r.Header.Get("Authorization")
		originator = r.Header.Get("originator")
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.output_item.done",
			`data: {"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"ENC-1"}}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	provider, err := New(Config{
		BaseURL:    server.URL,
		APIKey:     "access-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.Stream(t.Context(), &core.Request{
		Model: "gpt-5-codex",
		Messages: []core.Message{{
			Role:   core.RoleUser,
			Blocks: []core.Block{core.TextBlock{Text: "compact this"}},
		}},
		ProviderOptions: core.ProviderOptions{"compaction_trigger": true},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	response, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(response.CompactionItems) != 1 {
		t.Fatalf("CompactionItems = %d, want 1", len(response.CompactionItems))
	}
	if string(response.CompactionItems[0]) != `{"type":"compaction","encrypted_content":"ENC-1"}` {
		t.Fatalf("CompactionItems[0] = %s", response.CompactionItems[0])
	}
	if len(requestBody.Input) != 2 || !requestBody.Input[1].IsCompactionTrigger() {
		t.Fatalf("request input = %+v, want final compaction_trigger", requestBody.Input)
	}
	if authorization != "Bearer access-token" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if originator != DefaultOriginator {
		t.Fatalf("originator = %q, want %q", originator, DefaultOriginator)
	}
	if userAgent != CodexUserAgent {
		t.Fatalf("User-Agent = %q, want %q", userAgent, CodexUserAgent)
	}
}

func TestProviderStreamCompactionRequiresExactlyOneItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	provider, err := New(Config{BaseURL: server.URL, APIKey: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.Stream(t.Context(), &core.Request{
		Model:           "gpt-5-codex",
		ProviderOptions: core.ProviderOptions{"compaction_trigger": true},
		Messages:        []core.Message{{Role: core.RoleUser, Blocks: []core.Block{core.TextBlock{Text: "x"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if _, err := core.Collect(stream); err == nil || !strings.Contains(err.Error(), "exactly one compaction") {
		t.Fatalf("Collect error = %v, want exactly-one compaction error", err)
	}
}

func TestProviderPreservesPostCompactionToolRoundInCausalOrder(t *testing.T) {
	var requestBody ResponsesAPIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: response.output_item.done",
			`data: {"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"ENC-2"}}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	provider, err := New(Config{BaseURL: server.URL, APIKey: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.Stream(t.Context(), &core.Request{
		Model: "gpt-5-codex",
		Messages: []core.Message{
			core.User(core.TextBlock{Text: "retained question"}),
			core.User(core.TextBlock{Text: "continue after compaction"}),
			core.Assistant(core.ToolUseBlock{ID: "call_1", Name: "skill", Arguments: json.RawMessage(`{"query":"go"}`)}),
			core.ToolResult("call_1", core.TextBlock{Text: "skill loaded"}),
		},
		ProviderOptions: core.ProviderOptions{
			"compaction_items":           `[{"type":"compaction","encrypted_content":"ENC-1"}]`,
			"compaction_prefix_messages": 1,
			"compaction_trigger":         true,
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if _, err := core.Collect(stream); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(requestBody.Input) != 6 {
		t.Fatalf("request input = %+v, want retained user, checkpoint, user, call, output, and next compaction trigger", requestBody.Input)
	}
	if !requestBody.Input[0].IsUserMessage() || !requestBody.Input[1].IsCompaction() ||
		!requestBody.Input[2].IsUserMessage() || requestBody.Input[3].Type != ItemTypeFunctionCall ||
		requestBody.Input[4].Type != ItemTypeFunctionCallOutput || !requestBody.Input[5].IsCompactionTrigger() {
		t.Fatalf("request input = %+v, want retained user -> checkpoint -> post-checkpoint tool round -> compaction trigger", requestBody.Input)
	}
}

func TestProviderRetriesRetryableCompactionStream(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(strings.Join([]string{
				"event: response.created",
				`data: {"type":"response.created","response":{"id":"resp_retry"}}`,
				"",
			}, "\n")))
			return
		}
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_ok"}}`,
			"",
			"event: response.output_item.done",
			`data: {"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"ENC-RETRY"}}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_ok"}}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	provider, err := New(Config{BaseURL: server.URL, APIKey: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.Stream(t.Context(), &core.Request{
		Model:           "gpt-5-codex",
		Messages:        []core.Message{{Role: core.RoleUser, Blocks: []core.Block{core.TextBlock{Text: "x"}}}},
		ProviderOptions: core.ProviderOptions{"compaction_trigger": true},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	response, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("requests = %d, want one retry", calls.Load())
	}
	if len(response.CompactionItems) != 1 || !strings.Contains(string(response.CompactionItems[0]), "ENC-RETRY") {
		t.Fatalf("CompactionItems = %s, want retry result", response.CompactionItems)
	}
}

func TestCoreToolsPreservesExplicitStrictMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode core.StrictMode
		want string
	}{
		{"default", core.StrictDefault, ""}, {"disabled", core.StrictDisabled, "false"}, {"enabled", core.StrictEnabled, "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := core.Schema(`{"type":"object","properties":{"arguments_json":{"type":"object","properties":{}}}}`)
			wire := coreTools([]core.Tool{{Name: "mcp_call", Parameters: schema, Strict: tc.mode}})
			var got map[string]json.RawMessage
			if err := json.Unmarshal(wire[0], &got); err != nil {
				t.Fatal(err)
			}
			if string(got["strict"]) != tc.want {
				t.Fatalf("strict = %s, want %q", got["strict"], tc.want)
			}
			if string(got["parameters"]) != string(schema) {
				t.Fatalf("schema changed: %s", got["parameters"])
			}
		})
	}
}
