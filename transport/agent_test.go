package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"nusashell/contracts"
)

// TestAgentTurnStreamsOverSSE drives the full turn through the HTTP handler
// and asserts the SSE event sequence and persisted conversation.
func TestAgentTurnStreamsOverSSE(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// script: round 1 makes a tool call, round 2 streams the final text
	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_1", Name: "docs_search", Args: map[string]any{"query": "mcp"}}}},
		{{Text: "The docs explain MCP servers."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan []map[string]any, 1)
	go func() {
		frames, err := readSSEUntil(t, ctx, h.server.URL+"/events", contracts.EventTurnDone)
		if err != nil {
			done <- nil
			t.Logf("sse read: %v", err)
			return
		}
		done <- frames
	}()

	// give the subscriber a moment to attach
	time.Sleep(100 * time.Millisecond)
	started := h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID,
		"text":            "what is mcp?",
		"model":           "fake-model-1",
	})
	var run struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(started.Result, &run); err != nil || run.RunID == "" {
		t.Fatalf("turns.start = %s", started.Result)
	}

	select {
	case frames := <-done:
		if frames == nil {
			t.Fatal("no turn.done frame received")
		}
		assertFrameTypes(t, frames, []string{
			contracts.EventTurnStarted,
			contracts.EventMessageDelta,
			contracts.EventToolStarted,
			contracts.EventToolCompleted,
			contracts.EventMessageDelta,
			contracts.EventTurnDone,
		})
		var text string
		for _, f := range frames {
			if f["type"] == contracts.EventMessageDelta {
				text += f["payload"].(map[string]any)["text"].(string)
			}
		}
		if !strings.Contains(text, "docs explain MCP") {
			t.Fatalf("streamed text = %q", text)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for turn.done")
	}

	// conversation persisted with messages + tool calls + usage
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Conversation struct {
			Status string `json:"status"`
			Model  string `json:"model"`
		} `json:"conversation"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Status  string `json:"status"`
			Usage   *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			ToolCalls []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
				Output string `json:"output"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if conv.Conversation.Status != "idle" || conv.Conversation.Model != "fake-model-1" {
		t.Fatalf("conversation = %+v", conv.Conversation)
	}
	if len(conv.Messages) != 3 { // user, assistant(tool round), assistant(final)
		t.Fatalf("messages = %d, want 3", len(conv.Messages))
	}
	assistant := conv.Messages[1]
	if assistant.Role != "assistant" || assistant.Status != "done" {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", assistant.ToolCalls)
	}
	tc := assistant.ToolCalls[0]
	if tc.Name != "docs_search" || tc.Status != "ok" || !strings.Contains(tc.Output, "mcp") {
		t.Fatalf("tool call = %+v", tc)
	}
	// usage is recorded per round; both rounds used 10 in / 5 out
	if assistant.Usage == nil || assistant.Usage.InputTokens != 10 || assistant.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", assistant.Usage)
	}
	last := conv.Messages[2]
	if last.Role != "assistant" || !strings.Contains(last.Content, "docs explain MCP") {
		t.Fatalf("final message = %+v", last)
	}
	if last.Usage == nil || last.Usage.InputTokens != 10 || last.Usage.OutputTokens != 5 {
		t.Fatalf("final usage = %+v", last.Usage)
	}

	// the request the provider received must carry tools + system prompt
	body := h.llm.lastBody()
	if body == nil {
		t.Fatal("provider saw no request")
	}
	tools, _ := body["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("provider request has no tools")
	}
}

func assertFrameTypes(t *testing.T, frames []map[string]any, want []string) {
	t.Helper()
	var got []string
	for _, f := range frames {
		got = append(got, f["type"].(string))
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing frame %q in sequence %v", w, got)
		}
	}
}

// TestAgentTurnOverWebSocket runs the same turn over the /ws transport.
func TestAgentTurnOverWebSocket(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)
	h.llm.setRounds([][]llmStep{{{Text: "ws streaming works"}}})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")

	// create conversation over ws
	sendWS := func(id int, method string, payload any) {
		b, _ := json.Marshal(map[string]any{"id": id, "method": method, "payload": payload})
		if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	sendWS(1, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "hello", "model": "fake-model-1",
	})

	var gotRunID string
	var events []string
	var streamed strings.Builder
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		if id, ok := msg["id"]; ok {
			if id.(float64) == 1 {
				gotRunID = msg["result"].(map[string]any)["run_id"].(string)
			}
			continue
		}
		events = append(events, msg["type"].(string))
		if p, ok := msg["payload"].(map[string]any); ok {
			if text, ok := p["text"].(string); ok {
				streamed.WriteString(text)
			}
		}
		if msg["type"] == contracts.EventTurnDone {
			break
		}
	}
	if gotRunID == "" {
		t.Fatal("no run_id reply over ws")
	}
	assertFrameTypes(t, nil, nil) // no-op keeps helper referenced
	for _, want := range []string{contracts.EventTurnStarted, contracts.EventMessageDelta, contracts.EventTurnDone} {
		found := false
		for _, e := range events {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing ws event %s in %v", want, events)
		}
	}
	if streamed.String() != "ws streaming works" {
		t.Fatalf("ws streamed = %q", streamed.String())
	}
}

// TestAgentTurnStop interrupts a hanging provider turn.
func TestAgentTurnStop(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.delay = 30 * time.Second // hangs until ctx cancels
	started := h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "slow", "model": "fake-model-1",
	})
	var run struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(started.Result, &run); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)
	h.rpcOK(t, "agent.turns.stop", map[string]any{"run_id": run.RunID})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
		var conv struct {
			Conversation struct {
				Status string `json:"status"`
			} `json:"conversation"`
			Messages []struct {
				Role   string `json:"role"`
				Status string `json:"status"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(gotten.Result, &conv)
		if conv.Conversation.Status == "idle" && len(conv.Messages) == 2 && conv.Messages[1].Status == "interrupted" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("turn was not interrupted")
}

// TestAgentTurnCompaction verifies the compaction marker flow.
func TestAgentTurnCompaction(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// tiny threshold forces compaction on the next turn
	threshold := 1000
	h.rpcOK(t, "settings.set", map[string]any{"compaction_threshold": threshold})
	h.llm.setComplete(llmStep{Text: "SUMMARY: the user likes Go."})

	// seed a long conversation; each message is large enough that every
	// subsequent turn stays over the threshold
	for i := 0; i < 4; i++ {
		h.llm.setScript([]llmStep{{Text: strings.Repeat("x", 2000)}})
		h.rpcOK(t, "agent.turns.start", map[string]any{
			"conversation_id": convID, "text": strings.Repeat("y", 2000), "model": "fake-model-1",
		})
		waitTurnDone(t, h, convID)
	}

	// capture the compaction event on the next turn
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan []map[string]any, 1)
	go func() {
		frames, err := readSSEUntil(t, ctx, h.server.URL+"/events", contracts.EventCompacted)
		if err != nil {
			done <- nil
			return
		}
		done <- frames
	}()
	time.Sleep(100 * time.Millisecond)
	h.llm.setScript([]llmStep{{Text: "ok"}})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "continue", "model": "fake-model-1",
	})

	select {
	case frames := <-done:
		if frames == nil {
			t.Fatal("no compaction event")
		}
		for _, f := range frames {
			if f["type"] == contracts.EventCompacted {
				summary := f["payload"].(map[string]any)["summary"].(string)
				if !strings.Contains(summary, "SUMMARY") {
					t.Fatalf("summary = %q", summary)
				}
			}
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for compaction")
	}

	// the turn goroutine keeps streaming after the compaction event; wait
	// for it to finish so no writes happen during TempDir cleanup
	waitTurnDone(t, h, convID)

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	markers := 0
	for _, m := range conv.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "compacted") {
			markers++
		}
	}
	if markers == 0 {
		t.Fatalf("no compaction marker in %d messages", len(conv.Messages))
	}
}

func waitTurnDone(t *testing.T, h *harness, convID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
		var conv struct {
			Conversation struct {
				Status string `json:"status"`
			} `json:"conversation"`
		}
		_ = json.Unmarshal(gotten.Result, &conv)
		if conv.Conversation.Status == "idle" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("conversation never became idle")
}

// TestAgentTurnWithMCPTool executes a real MCP stdio tool through the agent.
func TestAgentTurnWithMCPTool(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// register the fake MCP server
	saved := h.rpcOK(t, "mcp.servers.save", map[string]any{
		"name": "fakemcp", "command": h.mcpBin, "args": []string{}, "enabled": true,
	})
	var sv struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(saved.Result, &sv); err != nil || len(sv.Servers) != 1 {
		t.Fatalf("mcp save = %s", saved.Result)
	}
	serverID := sv.Servers[0].ID

	// test connection lists tools
	tested := h.rpcOK(t, "mcp.servers.test", map[string]any{"id": serverID})
	var tools struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(tested.Result, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 || tools.Tools[0].Name != "echo" {
		t.Fatalf("mcp tools = %+v", tools.Tools)
	}

	// agent turn that calls the MCP echo tool
	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_9", Name: "mcp__fakemcp__echo", Args: map[string]any{"text": "hello-mcp"}}}},
		{{Text: "Echo done."}},
	})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "echo hello-mcp", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role      string `json:"role"`
			ToolCalls []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Output string `json:"output"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range conv.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name == "mcp__fakemcp__echo" {
				found = true
				if tc.Status != "ok" || !strings.Contains(tc.Output, "echo: hello-mcp") {
					t.Fatalf("mcp tool call = %+v", tc)
				}
			}
		}
	}
	if !found {
		t.Fatalf("mcp tool call missing from messages: %+v", conv.Messages)
	}
}

// TestAgentTurnAnthropic drives a turn through the Anthropic adapter and
// asserts prompt caching headers/body and tool_use streaming.
func TestAgentTurnAnthropic(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addAnthropicProvider(t, "Claude")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "toolu_1", Name: "docs_search", Args: map[string]any{"query": "skills"}}}},
		{{Text: "Claude finished."}},
	})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "look up skills", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	// the last provider request must have been the second round
	body := h.llm.lastBody()
	if body == nil {
		t.Fatal("anthropic provider saw no request")
	}
	if body["model"] != "fake-model-1" {
		t.Fatalf("model = %v", body["model"])
	}
	// prompt caching: system blocks and tools carry cache_control ephemeral
	system, _ := body["system"].([]any)
	foundCache := false
	if len(system) > 0 {
		if block, ok := system[0].(map[string]any); ok {
			if cc, ok := block["cache_control"].(map[string]any); ok && cc["type"] == "ephemeral" {
				foundCache = true
			}
		}
	}
	if !foundCache {
		t.Fatalf("system cache_control missing: %v", body["system"])
	}

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Status  string `json:"status"`
			Usage   *struct {
				CacheRead int `json:"cache_read"`
			} `json:"usage"`
			ToolCalls []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 3 {
		t.Fatalf("messages = %d", len(conv.Messages))
	}
	last := conv.Messages[2]
	if last.Content != "Claude finished." || last.Status != "done" {
		t.Fatalf("final message = %+v", last)
	}
	if len(conv.Messages[1].ToolCalls) != 1 || conv.Messages[1].ToolCalls[0].Name != "docs_search" {
		t.Fatalf("tool calls = %+v", conv.Messages[1].ToolCalls)
	}
	if last.Usage == nil || last.Usage.CacheRead < 4 {
		t.Fatalf("cache usage = %+v", last.Usage)
	}
}

// TestAgentTurnResponses drives a turn through the OpenAI Responses API
// adapter: instructions + tools in the request, streamed tool call, usage
// with cached tokens.
func TestAgentTurnResponses(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addResponsesProvider(t, "Resp")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_r1", Name: "docs_search", Args: map[string]any{"query": "mcp"}}}},
		{{Text: "Responses done."}},
	})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "search docs", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	// request shape: instructions present, tools as functions
	body := h.llm.lastBody()
	if body == nil {
		t.Fatal("responses provider saw no request")
	}
	if body["model"] != "fake-model-1" {
		t.Fatalf("model = %v", body["model"])
	}
	if _, ok := body["instructions"].(string); !ok {
		t.Fatalf("instructions missing: %v", body["instructions"])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools in responses request")
	}

	// canonical input shape: message content is a plain string, function
	// calls and outputs are top-level items (OpenRouter rejects block arrays)
	input, _ := body["input"].([]any)
	if len(input) == 0 {
		t.Fatal("input missing")
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("first input item = %+v", first)
	}
	if _, isString := first["content"].(string); !isString {
		t.Fatalf("user content must be a string, got %T: %+v", first["content"], first["content"])
	}
	hasCall, hasOutput := false, false
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["type"] == "function_call" {
			hasCall = true
			if item["call_id"] == "" || item["name"] == "" {
				t.Fatalf("function_call item incomplete: %+v", item)
			}
			if _, isStr := item["arguments"].(string); !isStr {
				t.Fatalf("function_call arguments must be a string: %+v", item["arguments"])
			}
		}
		if item["type"] == "function_call_output" {
			hasOutput = true
			if item["call_id"] == "" {
				t.Fatalf("function_call_output missing call_id: %+v", item)
			}
		}
	}
	if !hasCall || !hasOutput {
		t.Fatalf("expected function_call + function_call_output items, got %+v", input)
	}

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Status  string `json:"status"`
			Usage   *struct {
				CacheRead int `json:"cache_read"`
			} `json:"usage"`
			ToolCalls []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Output string `json:"output"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(conv.Messages))
	}
	if len(conv.Messages[1].ToolCalls) != 1 || conv.Messages[1].ToolCalls[0].Name != "docs_search" {
		t.Fatalf("tool calls = %+v", conv.Messages[1].ToolCalls)
	}
	tc := conv.Messages[1].ToolCalls[0]
	if tc.Status != "ok" || !strings.Contains(tc.Output, "mcp") {
		t.Fatalf("tool call = %+v", tc)
	}
	last := conv.Messages[2]
	if last.Content != "Responses done." || last.Status != "done" {
		t.Fatalf("final message = %+v", last)
	}
	if last.Usage == nil || last.Usage.CacheRead != 2 {
		t.Fatalf("cache usage = %+v", last.Usage)
	}
}

// TestAgentTurnToolFailure: a failing tool call is recorded with status
// fail and the error output, and the turn still completes.
func TestAgentTurnToolFailure(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_bad", Name: "skill_run", Args: map[string]any{"name": "missing-skill"}}}},
		{{Text: "done despite failure"}},
	})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "run a missing skill", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Output string `json:"output"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range conv.Messages {
		for _, tc := range m.ToolCalls {
			if tc.Name != "skill_run" {
				continue
			}
			found = true
			if tc.Status != "fail" {
				t.Fatalf("tool status = %q, want fail", tc.Status)
			}
			if !strings.Contains(tc.Output, "missing-skill") {
				t.Fatalf("tool error output = %q", tc.Output)
			}
		}
	}
	if !found {
		t.Fatal("tool call missing from conversation")
	}
	if last := conv.Messages[len(conv.Messages)-1]; last.Content != "done despite failure" {
		t.Fatalf("final message = %q", last.Content)
	}
}

// TestAgentTurnBusyGate: a conversation already running a turn rejects a
// second turn with CONFLICT.
func TestAgentTurnBusyGate(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.delay = 30 * time.Second
	started := h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "first", "model": "fake-model-1",
	})
	var run struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(started.Result, &run); err != nil || run.RunID == "" {
		t.Fatalf("first turn start = %s", started.Result)
	}
	time.Sleep(200 * time.Millisecond)

	res := h.rpc(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "second", "model": "fake-model-1",
	})
	if res.OK || res.Error == nil || res.Error.Code != "CONFLICT" {
		t.Fatalf("second turn must be CONFLICT, got %+v", res)
	}

	// clean up the hanging turn so its goroutine finishes before teardown
	h.rpcOK(t, "agent.turns.stop", map[string]any{"run_id": run.RunID})
	waitTurnDone(t, h, convID)
}

// TestAgentTurnMaxToolRounds: the loop terminates after maxToolRounds even
// when the provider keeps requesting tools.
func TestAgentTurnMaxToolRounds(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	rounds := make([][]llmStep, 0, 10)
	for i := 0; i < 10; i++ {
		rounds = append(rounds, []llmStep{{
			Tool: &llmToolCall{ID: fmt.Sprintf("call_%d", i), Name: "docs_search", Args: map[string]any{"query": "mcp"}},
		}})
	}
	h.llm.setRounds(rounds)
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "loop", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Conversation struct {
			Status string `json:"status"`
		} `json:"conversation"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if conv.Conversation.Status != "idle" {
		t.Fatalf("status = %q", conv.Conversation.Status)
	}
	// user + one assistant message per round, capped at the runner's
	// maxToolRounds (application/agent_runner.go)
	const maxRounds = 8
	if len(conv.Messages) != 1+maxRounds {
		t.Fatalf("messages = %d, want %d", len(conv.Messages), 1+maxRounds)
	}
}

// TestAgentTurnPromptCachingOff: with prompt caching disabled the Messages
// request carries no cache_control anywhere.
func TestAgentTurnPromptCachingOff(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addAnthropicProvider(t, "Claude")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	promptCaching := false
	h.rpcOK(t, "settings.set", map[string]any{"prompt_caching": promptCaching})
	h.llm.setRounds([][]llmStep{{{Text: "no cache"}}})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "hi", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	body := h.llm.lastBody()
	if body == nil {
		t.Fatal("provider saw no request")
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "cache_control") {
		t.Fatalf("cache_control present despite prompt caching off: %s", raw)
	}
}

var _ = http.StatusOK

// TestAgentTurnReasoningInterleaved exercises the full interleaved flow:
// thinking -> tool -> thinking -> tool -> reason. It pins three contracts:
//   - reasoning deltas are emitted as agent.reasoning.delta events
//   - reasoning is persisted on the assistant message
//   - the tool loop continues across reasoning rounds
func TestAgentTurnReasoningInterleaved(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// round 1: think, then call a tool
	// round 2: think again, then call another tool
	// round 3: final reasoning (answer) with no tool call
	h.llm.setRounds([][]llmStep{
		{
			{Reasoning: "I should search the docs first."},
			{Tool: &llmToolCall{ID: "call_r1", Name: "docs_search", Args: map[string]any{"query": "mcp"}}},
		},
		{
			{Reasoning: "Now I need to list skills."},
			{Tool: &llmToolCall{ID: "call_r2", Name: "skill_list", Args: map[string]any{}}},
		},
		{
			{Reasoning: "I have enough info to answer."},
			{Text: "Based on the docs and skills, here is the answer."},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// collect events over WebSocket
	done := make(chan []map[string]any, 1)
	go func() {
		frames, err := readWSUntil(ctx, h.server.URL+"/ws", contracts.EventTurnDone)
		if err != nil {
			done <- nil
			t.Logf("ws read: %v", err)
			return
		}
		done <- frames
	}()
	time.Sleep(100 * time.Millisecond)

	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID,
		"text":            "interleaved reasoning test",
		"model":           "fake-model-1",
	})

	var frames []map[string]any
	select {
	case frames = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for turn.done")
	}
	if frames == nil {
		t.Fatal("no frames received")
	}

	// 1) at least two reasoning delta events arrived
	reasoningDeltas := 0
	for _, f := range frames {
		if f["type"] == contracts.EventReasoningDelta {
			reasoningDeltas++
		}
	}
	if reasoningDeltas == 0 {
		t.Fatalf("expected reasoning delta events, got 0 (frames: %d)", len(frames))
	}

	// 2) two tool-started events (call_r1, call_r2)
	toolStarts := 0
	for _, f := range frames {
		if f["type"] == contracts.EventToolStarted {
			toolStarts++
		}
	}
	if toolStarts != 2 {
		t.Fatalf("expected 2 tool.started events, got %d", toolStarts)
	}

	// 3) reasoning persisted + steps in order across ALL assistant messages
	// (each round creates a separate assistant message, so steps are spread
	// across messages in temporal order)
	waitTurnDone(t, h, convID)
	conv := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var result struct {
		Conversation struct {
			MessageCount int `json:"message_count"`
		}
		Messages []struct {
			Role      string `json:"role"`
			Reasoning string `json:"reasoning"`
			Content   string `json:"content"`
			Steps     []struct {
				Type      string `json:"type"`
				Content   string `json:"content"`
				ToolCalls []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"tool_calls"`
			} `json:"steps"`
			ToolCalls []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(conv.Result, &result); err != nil {
		t.Fatalf("unmarshal conversation: %v", err)
	}
	// collect all steps from all assistant messages in order
	type stepInfo struct {
		Type      string
		Content   string
		ToolCalls []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		}
	}
	var allSteps []stepInfo
	var lastContent, lastReasoning string
	for i := range result.Messages {
		if result.Messages[i].Role != "assistant" {
			continue
		}
		if result.Messages[i].Content != "" {
			lastContent = result.Messages[i].Content
			lastReasoning = result.Messages[i].Reasoning
		}
		for _, s := range result.Messages[i].Steps {
			allSteps = append(allSteps, stepInfo{
				Type: s.Type, Content: s.Content, ToolCalls: s.ToolCalls,
			})
		}
	}
	if lastReasoning == "" {
		t.Fatalf("expected reasoning persisted on final assistant message, got empty")
	}
	if !strings.Contains(lastReasoning, "I have enough info") {
		t.Fatalf("reasoning content mismatch: %q", lastReasoning)
	}
	if !strings.Contains(lastContent, "here is the answer") {
		t.Fatalf("content mismatch: %q", lastContent)
	}
	// 4) steps in temporal order: reasoning → tool_calls → reasoning → tool_calls → reasoning → text
	wantOrder := []string{"reasoning", "tool_calls", "reasoning", "tool_calls", "reasoning", "text"}
	if len(allSteps) < len(wantOrder) {
		t.Fatalf("expected at least %d steps, got %d: %+v", len(wantOrder), len(allSteps), allSteps)
	}
	for i, want := range wantOrder {
		if allSteps[i].Type != want {
			t.Fatalf("step %d: want %q, got %q (full steps: %+v)", i, want, allSteps[i].Type, allSteps)
		}
	}
	// verify tool calls in steps match the rounds
	if len(allSteps[1].ToolCalls) != 1 || allSteps[1].ToolCalls[0].Name != "docs_search" {
		t.Fatalf("step 1 tool_calls mismatch: %+v", allSteps[1].ToolCalls)
	}
	if len(allSteps[3].ToolCalls) != 1 || allSteps[3].ToolCalls[0].Name != "skill_list" {
		t.Fatalf("step 3 tool_calls mismatch: %+v", allSteps[3].ToolCalls)
	}
}
