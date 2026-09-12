package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildResponseRequestKeepsCacheKeyAndToolContract(t *testing.T) {
	request := buildResponseRequest("gpt-5.5", "webchat-test-session", nil)
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(payload)
	for _, expected := range []string{
		`"prompt_cache_key":"webchat-test-session"`,
		`"store":false`,
		`"name":"getCurrentTime"`,
		`"include":["reasoning.encrypted_content"]`,
	} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("request does not contain %s: %s", expected, encoded)
		}
	}
}

func TestValidateNoArguments(t *testing.T) {
	for _, arguments := range []string{"", "{}", " { } "} {
		if err := validateNoArguments(arguments); err != nil {
			t.Errorf("validateNoArguments(%q): %v", arguments, err)
		}
	}
	for _, arguments := range []string{`{"timezone":"Asia/Jakarta"}`, `[]`, `not-json`} {
		if err := validateNoArguments(arguments); err == nil {
			t.Errorf("validateNoArguments(%q) unexpectedly succeeded", arguments)
		}
	}
}

func TestExecuteFunctionCallReturnsTypedToolOutput(t *testing.T) {
	original := timeNow
	t.Cleanup(func() { timeNow = original })
	timeNow = func() currentTime {
		return currentTime{
			local:    "2026-09-13T20:00:00+07:00",
			timezone: "Asia/Jakarta",
			utc:      "2026-09-13T13:00:00Z",
		}
	}

	item, err := executeFunctionCall(functionCall{
		Name:      "getCurrentTime",
		CallID:    "call_123",
		Arguments: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]string
	if err := json.Unmarshal(item, &output); err != nil {
		t.Fatal(err)
	}
	if output["type"] != "function_call_output" || output["call_id"] != "call_123" {
		t.Fatalf("unexpected tool output envelope: %#v", output)
	}
	if !strings.Contains(output["output"], "Asia/Jakarta") || !strings.Contains(output["output"], "2026-09-13") {
		t.Fatalf("unexpected tool result: %#v", output)
	}
}

func TestResponseOutputTextWalksMessageContent(t *testing.T) {
	output := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","summary":[]}`),
		json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"hello"},{"type":"output_text","text":" world"}]}`),
	}
	text, err := responseOutputText(output)
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
}

func TestChatHandlerRunsToolLoopAndReplaysItems(t *testing.T) {
	requestCount := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var request responseRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.PromptCacheKey != "webchat-browser-test" {
			t.Errorf("prompt cache key = %q", request.PromptCacheKey)
		}
		if request.Store {
			t.Error("request unexpectedly enables server-side storage")
		}
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		switch requestCount {
		case 1:
			if len(request.Input) != 1 {
				t.Errorf("first input length = %d, want 1", len(request.Input))
			}
			_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"getCurrentTime","arguments":"{}"}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`))
		case 2:
			if len(request.Input) != 3 {
				t.Errorf("second input length = %d, want 3", len(request.Input))
			}
			wantTypes := []string{"message", "function_call", "function_call_output"}
			for i, item := range request.Input {
				var envelope struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(item, &envelope); err != nil {
					t.Errorf("decode input[%d]: %v", i, err)
					continue
				}
				if envelope.Type != wantTypes[i] {
					t.Errorf("input[%d].type = %q, want %q", i, envelope.Type, wantTypes[i])
				}
			}
			_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"It is demo time."}]}],"usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24}}`))
		default:
			t.Errorf("unexpected request %d", requestCount)
			http.Error(w, "too many requests", http.StatusInternalServerError)
		}
	}))
	defer api.Close()

	server := &chatServer{
		apiKey:   "test-key",
		model:    "gpt-5.5",
		baseURL:  api.URL,
		client:   api.Client(),
		sessions: &sessionStore{sessions: make(map[string]*chatSession)},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"session_id":"browser-test","message":"What time is it?"}`))
	recorder := httptest.NewRecorder()
	server.handleChat(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response chatResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Reply != "It is demo time." {
		t.Fatalf("reply = %q", response.Reply)
	}
	if response.Usage.TotalTokens != 36 {
		t.Fatalf("total tokens = %d, want 36", response.Usage.TotalTokens)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0] != "getCurrentTime" {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
}

func TestRoutesServeEmbeddedWebChat(t *testing.T) {
	server := &chatServer{}
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "OpenAI Responses web chat") {
		t.Fatal("embedded index.html was not served")
	}
}
