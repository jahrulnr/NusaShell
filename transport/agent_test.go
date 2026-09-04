package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

// readSSEUntilClosed consumes a /stream response until the server closes it
// (the round is sealed) or ctx expires. Returns the parsed frames in order.
func readSSEUntilClosed(ctx context.Context, url string) ([]map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s status %d", url, resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var frames []map[string]any
	var event, data string
	appendFrame := func() {
		if event == "" && data == "" {
			return
		}
		var payload map[string]any
		_ = json.Unmarshal([]byte(data), &payload)
		frames = append(frames, map[string]any{"type": event, "payload": payload})
		event, data = "", ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			appendFrame()
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	return frames, scanner.Err()
}

// TestAgentTurnStreamsOverSSE drives the full turn through the HTTP handler
// and asserts the SSE round-stream sequence and persisted conversation.
func TestAgentTurnStreamsOverSSE(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// script: round 1 makes a tool call, round 2 streams the final text
	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_1", Name: "docs", Args: map[string]any{"op": "search", "query": "mcp"}}}},
		{{Text: "The docs explain MCP servers."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan []map[string]any, 1)
	go func() {
		frames, err := readWSUntil(ctx, h.server.URL+"/ws", contracts.EventTurnDone)
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

	var doneMessageID string
	select {
	case frames := <-done:
		if frames == nil {
			t.Fatal("no turn.done frame received")
		}
		assertFrameTypes(t, frames, []string{
			contracts.EventTurnStarted,
			contracts.EventToolStarted,
			contracts.EventToolCompleted,
			contracts.EventTurnStarted,
			contracts.EventTurnDone,
		})
		// Text deltas travel the per-round SSE streams, not the WebSocket.
		var runID string
		var msgIDs []string
		for _, f := range frames {
			p := f["payload"].(map[string]any)
			switch f["type"] {
			case contracts.EventTurnStarted:
				if runID == "" {
					runID = p["run_id"].(string)
				}
				msgIDs = append(msgIDs, p["message_id"].(string))
			case contracts.EventTurnDone:
				doneMessageID = p["message_id"].(string)
			}
		}
		if runID == "" || len(msgIDs) != 2 {
			t.Fatalf("expected run_id and 2 turn.started message ids, got run=%q msgs=%v", runID, msgIDs)
		}
		// Round 1 only made a tool call: no deltas, seals with next chaining
		// to round 2's message.
		r1, err := readSSEUntilClosed(ctx, fmt.Sprintf("%s/stream?run_id=%s&message_id=%s", h.server.URL, runID, msgIDs[0]))
		if err != nil {
			t.Fatalf("round 1 stream: %v", err)
		}
		var nextMsg string
		for _, f := range r1 {
			if f["type"] == "round.done" {
				if state := f["payload"].(map[string]any)["state"]; state != "done" {
					t.Fatalf("round 1 state = %v", state)
				}
				if n, ok := f["payload"].(map[string]any)["next"].(map[string]any); ok {
					nextMsg = n["message_id"].(string)
				}
			}
		}
		if nextMsg != msgIDs[1] {
			t.Fatalf("round 1 next = %q, want %q", nextMsg, msgIDs[1])
		}
		// Round 2 streams the final text via round.delta frames.
		r2, err := readSSEUntilClosed(ctx, fmt.Sprintf("%s/stream?run_id=%s&message_id=%s", h.server.URL, runID, msgIDs[1]))
		if err != nil {
			t.Fatalf("round 2 stream: %v", err)
		}
		var streamed strings.Builder
		var doneCount int
		for _, f := range r2 {
			if f["type"] == "round.delta" {
				p := f["payload"].(map[string]any)
				if p["kind"] == "text" {
					streamed.WriteString(p["text"].(string))
				}
			}
			if f["type"] == "round.done" {
				doneCount++
				if n, ok := f["payload"].(map[string]any)["next"]; ok && n != nil {
					t.Fatalf("round 2 next should be nil, got %v", n)
				}
			}
		}
		if doneCount != 1 {
			t.Fatalf("round 2 done frames = %d", doneCount)
		}
		if !strings.Contains(streamed.String(), "docs explain MCP") {
			t.Fatalf("streamed text = %q", streamed.String())
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
			ID      string `json:"id"`
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
	if doneMessageID == "" || doneMessageID != conv.Messages[2].ID {
		t.Fatalf("turn.done message_id = %q, final message id = %q", doneMessageID, conv.Messages[2].ID)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", assistant.ToolCalls)
	}
	// Single naming layer: the provider emits {docs, op=search} and that
	// exact root name is what gets persisted and executed.
	tc := assistant.ToolCalls[0]
	if tc.Name != "docs" || tc.Status != "ok" || !strings.Contains(tc.Output, "mcp") {
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
	var msgID string
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
			if mid, ok := p["message_id"].(string); ok && msg["type"] == contracts.EventTurnStarted {
				msgID = mid
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
	for _, want := range []string{contracts.EventTurnStarted, contracts.EventTurnDone} {
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
	// Text deltas travel the per-round SSE stream. Attach with the message id
	// from turn.started and verify the final text.
	if msgID == "" {
		t.Fatal("no turn.started message_id captured over ws")
	}
	frames, err := readSSEUntilClosed(ctx, fmt.Sprintf("%s/stream?run_id=%s&message_id=%s", h.server.URL, gotRunID, msgID))
	if err != nil {
		t.Fatalf("round stream: %v", err)
	}
	var sseText strings.Builder
	for _, f := range frames {
		if f["type"] == "round.delta" {
			p := f["payload"].(map[string]any)
			if p["kind"] == "text" {
				sseText.WriteString(p["text"].(string))
			}
		}
	}
	if sseText.String() != "ws streaming works" {
		t.Fatalf("round stream text = %q", sseText.String())
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
		if conv.Conversation.Status == "idle" && len(conv.Messages) == 2 && conv.Messages[1].Status == "interrupted by user" {
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

	// Seed history with compaction disabled, then enable it with a small
	// context window so the next turn triggers compaction.
	h.rpcOK(t, "settings.set", map[string]any{"compaction_enabled": false})
	h.llm.setComplete(llmStep{Text: "SUMMARY: the user likes Go and wants to build a local AI shell with embedded frontend. Completed: research phase, architecture design, provider selection. Remaining: implement agent turn loop, wire up transports, write tests. Key decision: use Clean Architecture with domain/application/infrastructure layers. Path: /home/user/project/nusashell."})

	// seed a long conversation; each message is large enough to exceed the
	// trigger once compaction is enabled.
	for i := 0; i < 4; i++ {
		h.llm.setScript([]llmStep{{Text: strings.Repeat("x", 4000)}})
		h.rpcOK(t, "agent.turns.start", map[string]any{
			"conversation_id": convID, "text": strings.Repeat("y", 4000), "model": "fake-model-1",
		})
		waitTurnDone(t, h, convID)
	}

	// Enable compaction with a small context window (trigger = 800 tokens).
	// The seeded history (~16000 tokens) is well above the trigger.
	h.rpcOK(t, "settings.set", map[string]any{"compaction_enabled": true, "max_input_tokens": 1000})

	// capture the compaction event on the next turn
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan []map[string]any, 1)
	go func() {
		frames, err := readWSUntil(ctx, h.server.URL+"/ws", contracts.EventCompacted)
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
		seenCompacting := false
		compactingIndex := -1
		compactedIndex := -1
		for index, f := range frames {
			if f["type"] == contracts.EventCompacting {
				payload := f["payload"].(map[string]any)
				if payload["conversation_id"] != convID {
					t.Fatalf("compacting event conversation_id = %v, want %s", payload["conversation_id"], convID)
				}
				if payload["run_id"] == "" {
					t.Fatal("compacting event missing run_id")
				}
				seenCompacting = true
				compactingIndex = index
			}
			if f["type"] == contracts.EventCompacted {
				compactedIndex = index
				payload := f["payload"].(map[string]any)
				if payload["run_id"] == "" {
					t.Fatal("compaction event missing run_id")
				}
				summary := payload["summary"].(string)
				if !strings.Contains(summary, "SUMMARY") {
					t.Fatalf("summary = %q", summary)
				}
			}
		}
		if !seenCompacting {
			t.Fatal("compaction did not emit agent.compacting before agent.compacted")
		}
		if compactingIndex < 0 || compactedIndex < 0 || compactingIndex >= compactedIndex {
			t.Fatalf("compaction lifecycle order = compacting:%d compacted:%d, want compacting before compacted", compactingIndex, compactedIndex)
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
		if m.Role == "user" && strings.HasPrefix(m.Content, "[COMPACTION CHECKPOINT]") {
			markers++
		}
	}
	if markers == 0 {
		t.Fatalf("no compaction marker in %d messages", len(conv.Messages))
	}
}

// TestAgentTurnMultiPassCompaction verifies that a conversation larger than the
// model's context window is compacted via multiple rolling passes, with no
// messages dropped. The conversation is seeded with enough content to require
// at least 2 compaction passes.
func TestAgentTurnMultiPassCompaction(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// Seed history with compaction disabled so Complete is only used on the
	// triggering turn. A small context window then forces multi-pass summary.
	h.rpcOK(t, "settings.set", map[string]any{
		"compaction_enabled": false,
		"max_input_tokens":   5000,
	})

	// Seed 4 turns with large messages: 8 messages × ~2000 tokens = ~16000 tokens.
	// keep budget = min(64000, 5000*0.3) = 1500 → splitIdx keeps ~1 message.
	// toCompact = ~7 messages × ~2000 = ~14000 tokens.
	// available = 5000 - 300 - 2000 - 800 = 1900 → multiple passes.
	bigMsg := strings.Repeat("x", 8000)
	for i := 0; i < 4; i++ {
		h.llm.setScript([]llmStep{{Text: bigMsg}})
		h.rpcOK(t, "agent.turns.start", map[string]any{
			"conversation_id": convID, "text": bigMsg, "model": "fake-model-1",
		})
		waitTurnDone(t, h, convID)
	}

	h.rpcOK(t, "settings.set", map[string]any{"compaction_enabled": true})

	// Set up the compaction summary response and the final turn response.
	h.llm.setComplete(llmStep{Text: "SUMMARY: compacted pass with enough detail to pass the quality guard. Goal: build local AI shell. Completed: research, architecture design, provider selection. Remaining: implement agent turn loop, wire transports, write tests. Key decision: Clean Architecture with domain/application/infrastructure layers. Path: /home/user/project/nusashell."})
	h.llm.setScript([]llmStep{{Text: "ok"}})

	// Count non-streaming requests before the triggering turn.
	completeBefore := h.llm.completeRequestCount()

	// Trigger compaction with a new user message.
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "continue", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	// Count non-streaming requests after — each compaction pass is one.
	compactionPasses := h.llm.completeRequestCount() - completeBefore
	if compactionPasses < 2 {
		t.Fatalf("expected at least 2 multi-pass compaction requests, got %d", compactionPasses)
	}

	// Verify the conversation has a compaction marker with the summary.
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
	hasMarker := false
	for _, m := range conv.Messages {
		if m.Role == "user" && strings.HasPrefix(m.Content, "[COMPACTION CHECKPOINT]") {
			hasMarker = true
			if !strings.Contains(m.Content, "SUMMARY") {
				t.Fatalf("compaction marker missing summary: %q", m.Content)
			}
		}
	}
	if !hasMarker {
		t.Fatalf("no compaction marker in %d messages", len(conv.Messages))
	}
}

// TestAgentTurnSteer verifies that a user message queued mid-turn via
// agent.turns.steer is injected at the next tool round boundary and appears
// in the persisted conversation with the steer flag.
func TestAgentTurnSteer(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// Round 1: make a tool call (keeps the turn alive so we can steer).
	// Round 2: final text response (after steer is injected).
	// Round 3: final text response (in case the steer triggers another tool round).
	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_1", Name: "docs", Args: map[string]any{"op": "search", "query": "test"}}}},
		{{Text: "Done after steer."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Start listening for steer events before starting the turn.
	steerApplied := make(chan map[string]any, 1)
	go func() {
		frames, err := readWSUntil(ctx, h.server.URL+"/ws", contracts.EventSteerApplied)
		if err != nil {
			steerApplied <- nil
			return
		}
		for _, f := range frames {
			if f["type"] == contracts.EventSteerApplied {
				steerApplied <- f
				return
			}
		}
		steerApplied <- nil
	}()
	time.Sleep(100 * time.Millisecond)

	// Start the turn (round 1 will make a tool call, keeping the turn alive).
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "search for test", "model": "fake-model-1",
	})

	// Wait for the turn to be running (tool call in progress).
	waitTurnRunning(t, h, convID)

	// Queue a steer message while the turn is running.
	steerRes := h.rpcOK(t, "agent.turns.steer", map[string]any{
		"conversation_id": convID, "text": "Actually, focus on the API docs.",
	})
	var steerResp struct {
		SteerID  string `json:"steer_id"`
		Accepted bool   `json:"accepted"`
	}
	if err := json.Unmarshal(steerRes.Result, &steerResp); err != nil {
		t.Fatal(err)
	}
	if !steerResp.Accepted || steerResp.SteerID == "" {
		t.Fatalf("steer not accepted: %+v", steerResp)
	}

	// Wait for the steer to be applied (emitted at the tool round boundary).
	select {
	case f := <-steerApplied:
		if f == nil {
			t.Fatal("steer applied event not received")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for steer applied event")
	}

	// Wait for the turn to finish.
	waitTurnDone(t, h, convID)

	// Verify the steer message is in the conversation with the steer flag.
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Steer   bool   `json:"steer"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	foundSteer := false
	for _, m := range conv.Messages {
		if m.Role == "user" && m.Steer && strings.Contains(m.Content, "focus on the API docs") {
			foundSteer = true
		}
	}
	if !foundSteer {
		t.Fatalf("steer message not found in conversation: %+v", conv.Messages)
	}
}

// TestAgentTurnSteerRejectedWhenIdle verifies that steering a conversation
// with no active turn returns a conflict error.
func TestAgentTurnSteerRejectedWhenIdle(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	res := h.rpc(t, "agent.turns.steer", map[string]any{
		"conversation_id": convID, "text": "steer when idle",
	})
	if res.OK {
		t.Fatal("expected conflict error for steer on idle conversation")
	}
	if res.Error == nil || res.Error.Code != string(contracts.CodeConflict) {
		t.Fatalf("expected conflict error, got %+v", res)
	}
}

// TestAgentTurnSteerAppliedOnNoToolCallExit verifies that a steer queued while
// the model is producing a text-only response (no tool calls) is still applied
// before the turn ends. Previously the steer was silently cancelled because
// drainSteer was only called after tool execution, not before the no-tool-calls
// break.
func TestAgentTurnSteerAppliedOnNoToolCallExit(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// Round 1: text-only response (no tool calls). The turn would normally
	// end here, but a queued steer should be drained and the model given a
	// new round to respond to it.
	// Round 2: text-only response to the steer.
	h.llm.setRounds([][]llmStep{
		{{Text: "Here is your answer."}},
		{{Text: "Acknowledged the steer."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	steerApplied := make(chan map[string]any, 1)
	go func() {
		frames, err := readWSUntil(ctx, h.server.URL+"/ws", contracts.EventSteerApplied)
		if err != nil {
			steerApplied <- nil
			return
		}
		for _, f := range frames {
			if f["type"] == contracts.EventSteerApplied {
				steerApplied <- f
				return
			}
		}
		steerApplied <- nil
	}()
	time.Sleep(100 * time.Millisecond)

	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "tell me something", "model": "fake-model-1",
	})

	// Wait for the turn to be running, then queue a steer.
	waitTurnRunning(t, h, convID)
	h.rpcOK(t, "agent.turns.steer", map[string]any{
		"conversation_id": convID, "text": "Wait, actually tell me about cats.",
	})

	// The steer should be applied even though round 1 had no tool calls.
	select {
	case f := <-steerApplied:
		if f == nil {
			t.Fatal("steer applied event not received")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for steer applied event (steer was not drained on no-tool-call exit)")
	}

	waitTurnDone(t, h, convID)

	// Verify the steer message is in the conversation.
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Steer   bool   `json:"steer"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	foundSteer := false
	for _, m := range conv.Messages {
		if m.Role == "user" && m.Steer && strings.Contains(m.Content, "tell me about cats") {
			foundSteer = true
		}
	}
	if !foundSteer {
		t.Fatalf("steer message not found in conversation: %+v", conv.Messages)
	}
}

func waitTurnRunning(t *testing.T, h *harness, convID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
		var conv struct {
			Conversation struct {
				Status string `json:"status"`
			} `json:"conversation"`
		}
		_ = json.Unmarshal(gotten.Result, &conv)
		if conv.Conversation.Status == "running" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("turn did not reach running state")
}

func waitTurnDone(t *testing.T, h *harness, convID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
		var conv struct {
			Conversation struct {
				Status string `json:"status"`
			} `json:"conversation"`
		}
		_ = json.Unmarshal(gotten.Result, &conv)
		if conv.Conversation.Status != "idle" {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		// The conversation reports idle but the run map may not be cleared
		// yet (async cleanup). Confirm no active run exists before returning,
		// otherwise the next agent.turns.start can race with the tail of the
		// previous turn and fail with "conversation is busy" on slow runners.
		active := h.rpcOK(t, "agent.turns.active", map[string]any{"id": convID})
		var act struct {
			Active bool `json:"active"`
		}
		_ = json.Unmarshal(active.Result, &act)
		if !act.Active {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("conversation never became idle")
}

// TestAgentTurnWithMCPTool executes a real MCP stdio tool through the agent.
// MCP tools are not advertised in the tool list (prompt cache stability), but
// the agent can still discover them via tool_list / tool_schema and call them
// by name — execution validates against the connected MCP server.
func TestAgentTurnWithMCPTool(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// register the fake MCP server
	saved := h.rpcOK(t, "plugin.save", map[string]any{
		"name": "fakemcp", "command": h.mcpBin, "args": []string{}, "enabled": true,
	})
	var sv struct {
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(saved.Result, &sv); err != nil || len(sv.Plugins) != 1 {
		t.Fatalf("plugin save = %s", saved.Result)
	}
	serverID := sv.Plugins[0].ID

	// test connection lists tools
	tested := h.rpcOK(t, "plugin.test", map[string]any{"id": serverID})
	var tools struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(tested.Result, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 3 || tools.Tools[0].Name != "echo" {
		t.Fatalf("mcp tools = %+v", tools.Tools)
	}

	// agent turn that calls the MCP echo tool through the single execution
	// contract: mcp_call with a ref (not advertised in tools[], but
	// discoverable via mcp_search / tool_list).
	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_9", Name: "mcp_call", Args: map[string]any{"ref": serverID + ":echo", "arguments_json": "{\"text\":\"hello-mcp\"}"}}}},
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
			if tc.Name == "mcp_call" {
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
	// Stop the plugin's MCP subprocess so TempDir cleanup does not fail on
	// Windows, where a running child process locks the plugin directory.
	h.rpcOK(t, "plugin.stop", map[string]any{"id": serverID})
}

// TestAgentTurnAnthropic drives a turn through the Anthropic adapter and
// asserts prompt caching headers/body and tool_use streaming.
func TestAgentTurnAnthropic(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addAnthropicProvider(t, "Claude")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "toolu_1", Name: "docs", Args: map[string]any{"op": "search", "query": "skills"}}}},
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
	if len(conv.Messages[1].ToolCalls) != 1 || conv.Messages[1].ToolCalls[0].Name != "docs" {
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
		{{Tool: &llmToolCall{ID: "call_r1", Name: "docs", Args: map[string]any{"op": "search", "query": "mcp"}}}},
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

	// canonical input shape: message content items, function calls and
	// outputs are top-level items. The ported litellm responses builder
	// emits content as an array of input_text items (the Responses API
	// accepts both shapes; OpenRouter rejects bare block arrays only for
	// tool results, which stay top-level items).
	input, _ := body["input"].([]any)
	if len(input) == 0 {
		t.Fatal("input missing")
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("first input item = %+v", first)
	}
	contentItems, ok := first["content"].([]any)
	if !ok || len(contentItems) == 0 {
		t.Fatalf("user content must be an array of input_text items, got %T: %+v", first["content"], first["content"])
	}
	if item, ok := contentItems[0].(map[string]any); !ok || item["type"] != "input_text" {
		t.Fatalf("first content item = %+v", contentItems[0])
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
	if len(conv.Messages[1].ToolCalls) != 1 || conv.Messages[1].ToolCalls[0].Name != "docs" {
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
		{{Tool: &llmToolCall{ID: "call_bad", Name: "skill", Args: map[string]any{"op": "save", "id": "missing-skill", "name": "missing-skill", "content": "x"}}}},
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
			if tc.Name != "skill" {
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
	h.rpcOK(t, "settings.set", map[string]any{"max_tool_rounds": 2})

	rounds := make([][]llmStep, 0, 3)
	for i := 0; i < 2; i++ {
		rounds = append(rounds, []llmStep{{
			Tool: &llmToolCall{ID: fmt.Sprintf("call_%d", i), Name: "docs", Args: map[string]any{"op": "search", "query": "mcp"}},
		}})
	}
	rounds = append(rounds, []llmStep{{Text: "Tool limit reached; here is the final answer."}})
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
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if conv.Conversation.Status != "idle" {
		t.Fatalf("status = %q", conv.Conversation.Status)
	}
	// The agent executes exactly the stored number of tool rounds, then asks
	// the provider for a final answer with tools withheld.
	const maxRounds = 2
	if len(conv.Messages) != 1+maxRounds+1 {
		t.Fatalf("messages = %d, want %d", len(conv.Messages), 1+maxRounds+1)
	}
	if got := conv.Messages[len(conv.Messages)-1].Content; got != "Tool limit reached; here is the final answer." {
		t.Fatalf("final answer = %q", got)
	}
}

func TestAgentTurnRetriesTransientUpstreamFailureWithoutResettingTheTurn(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)
	h.llm.failOnce(http.StatusServiceUnavailable, http.Header{"Retry-After": []string{"0"}})
	h.llm.setScript([]llmStep{{Text: "Recovered after retry."}})
	requestsBefore := h.llm.requestCount()

	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "retry this", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	if got := h.llm.requestCount(); got != requestsBefore+2 {
		t.Fatalf("provider requests = %d, want %d", got, requestsBefore+2)
	}
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 || conv.Messages[1].Status != "done" || conv.Messages[1].Content != "Recovered after retry." {
		t.Fatalf("conversation after retry = %+v", conv.Messages)
	}
}

func TestAgentTurnRetriesTransientCompactionFailure(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// Seed enough history while compaction is disabled, then enable it with
	// a small context window so the next turn must call Complete for a summary.
	h.rpcOK(t, "settings.set", map[string]any{"compaction_enabled": false})
	h.llm.setScript([]llmStep{{Text: strings.Repeat("x", 2000)}})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": strings.Repeat("y", 2000), "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)
	h.rpcOK(t, "settings.set", map[string]any{"compaction_enabled": true, "max_input_tokens": 1000})
	h.llm.failOnce(http.StatusServiceUnavailable, nil)
	h.llm.setComplete(llmStep{Text: "Recovered compaction summary with enough detail to pass the quality guard. Goal: test retry after transient failure. Completed: setup, seed history. Remaining: verify retry behavior, check request count. Key decision: compaction must retry on 503. Path: /home/user/project/nusashell."})
	h.llm.setScript([]llmStep{{Text: "Turn completed after compaction retry."}})
	requestsBefore := h.llm.requestCount()

	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "continue", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	if got := h.llm.requestCount(); got != requestsBefore+3 {
		t.Fatalf("provider requests = %d, want %d", got, requestsBefore+3)
	}
}

func TestAgentTurnDoesNotRetryPermanentUpstreamFailure(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)
	h.llm.failOnce(http.StatusBadRequest, nil)
	requestsBefore := h.llm.requestCount()

	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "do not retry", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	if got := h.llm.requestCount(); got != requestsBefore+1 {
		t.Fatalf("provider requests = %d, want %d", got, requestsBefore+1)
	}
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 || conv.Messages[1].Status != "error" || !strings.Contains(conv.Messages[1].Error, "HTTP 400") {
		t.Fatalf("conversation after permanent failure = %+v", conv.Messages)
	}
}

func TestAgentTurnContinuesAfterPartialTransientStreamFailure(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)
	h.llm.truncateNextOpenAIStream()
	h.llm.setRounds([][]llmStep{
		{{Text: "The answer starts here. "}},
		{{Text: "And continues after reconnecting."}},
	})
	requestsBefore := h.llm.requestCount()

	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "continue safely", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	if got := h.llm.requestCount(); got != requestsBefore+2 {
		t.Fatalf("provider requests = %d, want %d", got, requestsBefore+2)
	}
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	// With round-level continuation, the partial content from the first
	// (truncated) stream is accumulated and prepended to the continuation
	// text from the retry, producing a single seamless assistant message.
	if len(conv.Messages) != 2 || conv.Messages[1].Content != "The answer starts here. And continues after reconnecting." || conv.Messages[1].Status != "done" {
		t.Fatalf("partial stream was not continued: %+v", conv.Messages)
	}
}

func TestAgentTurnPartialStreamContinuationDoesNotConsumeToolRound(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)
	h.rpcOK(t, "settings.set", map[string]any{"max_tool_rounds": 1})
	h.llm.truncateNextOpenAIStream()
	h.llm.setRounds([][]llmStep{
		{{Text: "The answer starts here. "}},
		{{Tool: &llmToolCall{ID: "call_after_recovery", Name: "docs", Args: map[string]any{"op": "search", "query": "mcp"}}}},
		{{Text: "The tool result completes the answer."}},
	})
	requestsBefore := h.llm.requestCount()

	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "continue safely with a tool", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	if got := h.llm.requestCount(); got != requestsBefore+3 {
		t.Fatalf("provider requests = %d, want %d", got, requestsBefore+3)
	}
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Content   string `json:"content"`
			Status    string `json:"status"`
			ToolCalls []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	// With round-level continuation, the partial text from the truncated
	// first stream is accumulated into the assistant message that also
	// carries the tool call from the continuation retry. The tool round
	// is not consumed by the continuation — the model still gets its
	// tool round budget.
	if len(conv.Messages) != 3 || conv.Messages[2].Content != "The tool result completes the answer." || conv.Messages[2].Status != "done" {
		t.Fatalf("continued tool round = %+v", conv.Messages)
	}
	if len(conv.Messages[1].ToolCalls) != 1 || conv.Messages[1].ToolCalls[0].Name != "docs" || conv.Messages[1].ToolCalls[0].Status != "ok" {
		t.Fatalf("tool after recovery = %+v", conv.Messages[1].ToolCalls)
	}
	if conv.Messages[1].Content != "The answer starts here. " {
		t.Fatalf("partial content not accumulated: %q", conv.Messages[1].Content)
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
			{Tool: &llmToolCall{ID: "call_r1", Name: "docs", Args: map[string]any{"op": "search", "query": "mcp"}}},
		},
		{
			{Reasoning: "Now I need to list skills."},
			{Tool: &llmToolCall{ID: "call_r2", Name: "skill", Args: map[string]any{"op": "list"}}},
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

	// 1) reasoning deltas arrive via the per-round SSE streams: attach to
	// each round's message (ids from turn.started) and collect the
	// kind=reasoning frames across all rounds.
	var runID string
	var msgIDs []string
	for _, f := range frames {
		p := f["payload"].(map[string]any)
		if f["type"] == contracts.EventTurnStarted {
			if runID == "" {
				runID = p["run_id"].(string)
			}
			msgIDs = append(msgIDs, p["message_id"].(string))
		}
	}
	if runID == "" || len(msgIDs) != 3 {
		t.Fatalf("expected run_id and 3 turn.started message ids, got run=%q msgs=%v", runID, msgIDs)
	}
	reasoningDeltas := 0
	var reasoningText strings.Builder
	for _, msgID := range msgIDs {
		sseFrames, err := readSSEUntilClosed(ctx, fmt.Sprintf("%s/stream?run_id=%s&message_id=%s", h.server.URL, runID, msgID))
		if err != nil {
			t.Fatalf("round stream %s: %v", msgID, err)
		}
		for _, f := range sseFrames {
			if f["type"] == "round.delta" {
				p := f["payload"].(map[string]any)
				if p["kind"] == "reasoning" {
					reasoningDeltas++
					reasoningText.WriteString(p["text"].(string))
				}
			}
		}
	}
	if reasoningDeltas == 0 {
		t.Fatalf("expected reasoning delta frames, got 0")
	}
	if !strings.Contains(reasoningText.String(), "Now I need to list skills") {
		t.Fatalf("reasoning delta text = %q", reasoningText.String())
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
	if len(allSteps[1].ToolCalls) != 1 || allSteps[1].ToolCalls[0].Name != "docs" {
		t.Fatalf("step 1 tool_calls mismatch: %+v", allSteps[1].ToolCalls)
	}
	if len(allSteps[3].ToolCalls) != 1 || allSteps[3].ToolCalls[0].Name != "skill" {
		t.Fatalf("step 3 tool_calls mismatch: %+v", allSteps[3].ToolCalls)
	}
}

// TestAgentTurnRetryWithDifferentModel: a turn that fails with a non-retryable
// 4xx can be retried with a different model picked by the user. The failed
// assistant stays in formed history; a new assistant is appended and completes.
func TestAgentTurnRetryWithDifferentModel(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.failOnce(http.StatusBadRequest, nil)
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "hello", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Model  string `json:"model"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 || conv.Messages[1].Status != "error" || !strings.Contains(conv.Messages[1].Error, "HTTP 400") {
		t.Fatalf("expected failed assistant message, got %+v", conv.Messages)
	}

	h.llm.setScript([]llmStep{{Text: "Recovered with model 2."}})
	h.rpcOK(t, "agent.turns.retry", map[string]any{
		"conversation_id": convID, "model": "fake-model-2",
	})
	waitTurnDone(t, h, convID)

	gotten = h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	conv = struct {
		Messages []struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Model  string `json:"model"`
		} `json:"messages"`
	}{}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) < 3 {
		t.Fatalf("expected user + failed assistant + retry assistant, got %+v", conv.Messages)
	}
	failed := conv.Messages[len(conv.Messages)-2]
	retried := conv.Messages[len(conv.Messages)-1]
	if failed.Status != "error" || !strings.Contains(failed.Error, "HTTP 400") {
		t.Fatalf("expected kept failed assistant, got %+v", conv.Messages)
	}
	if retried.Status != "done" {
		t.Fatalf("expected done after retry, got %+v", conv.Messages)
	}
	if retried.Model != "fake-model-2" {
		t.Fatalf("expected model fake-model-2, got %q", retried.Model)
	}

	res := h.rpc(t, "agent.turns.retry", map[string]any{
		"conversation_id": convID, "model": "fake-model-2",
	})
	if res.Error == nil || res.Error.Code != string(contracts.CodeNotFound) {
		t.Fatalf("expected NOT_FOUND after successful retry, got %+v", res.Error)
	}
}

// TestAgentTurnRetryRejectsWhenNoFailedTurn: retrying a conversation whose last
// turn succeeded returns a NOT_FOUND error instead of silently re-running.
func TestAgentTurnRetryRejectsWhenNoFailedTurn(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	h.llm.setScript([]llmStep{{Text: "All good."}})
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "hello", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	res := h.rpc(t, "agent.turns.retry", map[string]any{
		"conversation_id": convID, "model": "fake-model-2",
	})
	if res.OK {
		t.Fatalf("retry should fail when no failed turn exists, got result: %s", res.Result)
	}
	if res.Error == nil || res.Error.Code != string(contracts.CodeNotFound) {
		t.Fatalf("expected NOT_FOUND error, got %+v", res.Error)
	}
}

// TestAgentTurnRetryThenError verifies that when a retry also fails, the
// resulting assistant message has status "error" so the frontend can show
// the retry button again.
func TestAgentTurnRetryThenError(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// First turn: fail with 429 (all 3 auto-retry attempts)
	for i := 0; i < 3; i++ {
		h.llm.failOnce(http.StatusTooManyRequests, nil)
	}
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "hello", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	// Verify first failure
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role   string `json:"role"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 || conv.Messages[1].Status != "error" {
		t.Fatalf("expected failed assistant message after first error, got %+v", conv.Messages)
	}

	// Retry: also fail with 429 (all 3 auto-retry attempts)
	for i := 0; i < 3; i++ {
		h.llm.failOnce(http.StatusTooManyRequests, nil)
	}
	h.rpcOK(t, "agent.turns.retry", map[string]any{
		"conversation_id": convID, "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)

	// Verify retry failure: last assistant message should have status "error"
	gotten = h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	conv = struct {
		Messages []struct {
			Role   string `json:"role"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"messages"`
	}{}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	// Find the last assistant message
	var lastAssistant *struct {
		Role   string `json:"role"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	for i := range conv.Messages {
		if conv.Messages[i].Role == "assistant" {
			lastAssistant = &conv.Messages[i]
		}
	}
	if lastAssistant == nil {
		t.Fatal("no assistant message found")
	}
	if lastAssistant.Status != "error" {
		t.Fatalf("expected status 'error' after retry failure, got %q (messages: %+v)", lastAssistant.Status, conv.Messages)
	}
	if !strings.Contains(lastAssistant.Error, "rate-limited") {
		t.Fatalf("expected error to be rate-limited friendly message, got %q", lastAssistant.Error)
	}
}

// TestTurnContextTokensIsLastRoundNotSum proves the backend is the source of
// truth for the context badge: a two-round (tool) turn reports context_tokens
// equal to the LAST round's provider usage (input+output = 15), while the
// display usage sums both rounds (input 20 / output 10). The authoritative
// number is also persisted on the conversation for the idle badge.
func TestTurnContextTokensIsLastRoundNotSum(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Usage provider")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// Round 1 calls a tool; round 2 answers. The fake provider reports
	// prompt_tokens=10, completion_tokens=5 per request.
	h.llm.setRounds([][]llmStep{
		{{Tool: &llmToolCall{ID: "call_1", Name: "docs", Args: map[string]any{"op": "search", "query": "mcp"}}}},
		{{Text: "done"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan []map[string]any, 1)
	go func() {
		frames, err := readWSUntil(ctx, h.server.URL+"/ws", contracts.EventTurnDone)
		if err != nil {
			done <- nil
			return
		}
		done <- frames
	}()
	time.Sleep(100 * time.Millisecond)
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "what is mcp?", "model": "fake-model-1",
	})

	select {
	case frames := <-done:
		if frames == nil {
			t.Fatal("no turn.done frame received")
		}
		var payload map[string]any
		for _, f := range frames {
			if f["type"] == contracts.EventTurnDone {
				payload = f["payload"].(map[string]any)
			}
		}
		if payload == nil {
			t.Fatal("turn.done payload missing")
		}
		// context_tokens = last round only (10 + 5).
		if got := payload["context_tokens"]; got != float64(15) {
			t.Fatalf("turn.done context_tokens = %v, want 15 (last round, not summed)", got)
		}
		// usage still sums both rounds for the ↑/↓ display tags.
		usage := payload["usage"].(map[string]any)
		if usage["input_tokens"] != float64(20) || usage["output_tokens"] != float64(10) {
			t.Fatalf("turn.done usage = %v, want input 20 / output 10 (summed)", usage)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for turn.done")
	}

	// The idle badge source of truth is persisted on the conversation.
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Conversation struct {
			ContextTokens int64 `json:"context_tokens"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	if conv.Conversation.ContextTokens != 15 {
		t.Fatalf("persisted context_tokens = %d, want 15", conv.Conversation.ContextTokens)
	}
}

// TestAgentTurnRetryAfterAutoContinueRateLimit reproduces the exact repro for
// the "no failed assistant turn to retry" pop-up: turn 0 succeeds with open
// todos, the auto-continue chain starts turn 1 (a fresh assistant message),
// turn 1 hits a 429 rate limit and fails with the friendly message, then the
// user clicks Retry. The failed assistant message must still be in error
// status, so the retry must succeed instead of answering NOT_FOUND.
func TestAgentTurnRetryAfterAutoContinueRateLimit(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})
	convID := h.newConversation(t)

	// The harness does not wire a todo store; without open todos the
	// auto-continue chain never starts. Wire one and leave a todo open so
	// turn 0 chains into turn 1.
	h.app.Todos = jsonstore.NewTodoStore(filepath.Join(t.TempDir(), "todos.json"), t.TempDir(), nil)
	h.app.Todos.Set(convID, []domain.TodoItem{{ID: "t1", Content: "keep working", Status: domain.TodoInProgress}})

	// Turn 0 completes normally; the auto-continue turn 1 hits 429 on the
	// streaming request (mirrors OpenRouter's ~5 req/min window).
	h.llm.setRounds([][]llmStep{
		{{Text: "First answer."}},
		{{Text: "Second answer."}},
	})
	h.llm.failStatus = http.StatusTooManyRequests
	h.rpcOK(t, "agent.turns.start", map[string]any{
		"conversation_id": convID, "text": "hello", "model": "fake-model-1",
	})
	waitTurnDone(t, h, convID)
	h.llm.failStatus = 0 // clear before the retry below

	// The last assistant message is in error status with the friendly
	// rate-limit message (this is the state the frontend shows Retry on).
	gotten := h.rpcOK(t, "agent.conversations.get", map[string]any{"id": convID})
	var conv struct {
		Messages []struct {
			Role   string `json:"role"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotten.Result, &conv); err != nil {
		t.Fatal(err)
	}
	last := conv.Messages[len(conv.Messages)-1]
	if last.Role != "assistant" || last.Status != "error" {
		t.Fatalf("last message = %+v, want assistant/error (all: %+v)", last, conv.Messages)
	}
	if !strings.Contains(last.Error, "rate-limited") {
		t.Fatalf("last error = %q, want rate-limited message", last.Error)
	}

	// Retry the failed turn: must succeed, not answer NOT_FOUND.
	h.llm.setScript([]llmStep{{Text: "Recovered after rate limit."}})
	res := h.rpc(t, "agent.turns.retry", map[string]any{
		"conversation_id": convID, "model": "fake-model-2",
	})
	if !res.OK {
		t.Fatalf("retry after auto-continue rate-limit failure failed: %+v", res.Error)
	}
}
