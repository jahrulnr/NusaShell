package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"nusashell/infrastructure/ai/core"
)

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

func TestProviderRebuildsHistoryAroundOpaqueCompactionItem(t *testing.T) {
	var requestBody ResponsesAPIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
	}))
	defer server.Close()

	provider, err := New(Config{BaseURL: server.URL, APIKey: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream, err := provider.Stream(t.Context(), &core.Request{
		Model: "gpt-5-codex",
		Messages: []core.Message{
			{Role: core.RoleUser, Blocks: []core.Block{core.TextBlock{Text: "latest question"}}},
			{Role: core.RoleAssistant, Blocks: []core.Block{core.TextBlock{Text: "old answer"}}},
		},
		ProviderOptions: core.ProviderOptions{
			"compaction_items": `[{"type":"compaction","encrypted_content":"ENC-1"}]`,
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if _, err := core.Collect(stream); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(requestBody.Input) != 2 {
		t.Fatalf("request input = %+v, want latest user plus compaction", requestBody.Input)
	}
	if !requestBody.Input[0].IsUserMessage() || !requestBody.Input[1].IsCompaction() {
		t.Fatalf("request input = %+v, want user then opaque compaction item", requestBody.Input)
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
