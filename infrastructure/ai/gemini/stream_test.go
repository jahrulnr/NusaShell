package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func sseBody(chunks ...string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(strings.Join(chunks, "\n\n"))),
	}
}

func TestStreamAggregatesReasoningTextToolAndUsage(t *testing.T) {
	sse := sseBody(
		`data: {"candidates":[{"content":{"parts":[{"text":"I am thinking.","thought":true,"thoughtSignature":"sig-t"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"partial "}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"fc-1","name":"lookup","args":{"q":"x"}},"thoughtSignature":"sig-c"}]}}]}`,
		`data: {"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":6,"totalTokenCount":16}}`,
		`data: {"candidates":[{"finishReason":"STOP"}]}`,
	)
	req := &core.Request{Model: "gemini-2.5-flash", Thinking: &core.Thinking{Mode: core.ThinkingEnabled, Effort: "high"}}
	stream := newStream(sse, req, nil)
	defer stream.Close()

	var steps []core.Event
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		steps = append(steps, event)
	}
	types := eventTypes(steps)
	wantTypes := []string{
		"reasoning", "content", "content", "tool_start", "tool_delta", "tool_done", "usage", "done",
	}
	if strings.Join(types, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("event types = %v, want %v", types, wantTypes)
	}

	// The collected response carries reasoning text + signature, the text and
	// the tool call with its signature.
	resp, err := collectFromEvents(steps, req)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if resp.Reasoning() != "I am thinking." {
		t.Fatalf("reasoning = %q", resp.Reasoning())
	}
	var reasoningExtra json.RawMessage
	for _, block := range resp.Blocks {
		rb, ok := block.(core.ReasoningBlock)
		if !ok || len(rb.Extra) == 0 {
			continue
		}
		reasoningExtra = rb.Extra
	}
	if len(reasoningExtra) == 0 || !strings.Contains(string(reasoningExtra), "sig-t") {
		t.Fatalf("reasoning extra = %s, want signature replay payload", reasoningExtra)
	}
	if resp.Text() != "partial answer" {
		t.Fatalf("text = %q", resp.Text())
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "fc-1" || calls[0].Name != "lookup" || calls[0].Signature != "sig-c" {
		t.Fatalf("call = %+v", calls[0])
	}
	if resp.FinishReason != core.FinishReasonToolCall {
		t.Fatalf("finish = %s, want tool_calls", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 6 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

// collectFromEvents feeds recorded events through a fresh stream so the
// aggregation paths (including signature-extra merging) are exercised exactly
// as core.Handle consumes them.
func collectFromEvents(events []core.Event, req *core.Request) (*core.Response, error) {
	stream := &replayStream{events: events}
	resp, err := core.Collect(stream)
	if err != nil {
		return nil, err
	}
	resp.Model = req.Model
	resp.Provider = providerName
	return resp, nil
}

type replayStream struct {
	events []core.Event
}

func (s *replayStream) Next() (core.Event, error) {
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *replayStream) Close() error { return nil }

func eventTypes(events []core.Event) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		switch event.(type) {
		case core.ContentDelta:
			names = append(names, "content")
		case core.ReasoningDelta:
			names = append(names, "reasoning")
		case core.ToolUseStart:
			names = append(names, "tool_start")
		case core.ToolUseDelta:
			names = append(names, "tool_delta")
		case core.ToolUseDone:
			names = append(names, "tool_done")
		case core.UsageEvent:
			names = append(names, "usage")
		case core.DoneEvent:
			names = append(names, "done")
		case core.WarningEvent:
			names = append(names, "warning")
		default:
			names = append(names, "other")
		}
	}
	return names
}

func TestStreamMidStreamErrorChunk(t *testing.T) {
	sse := sseBody(`data: {"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`)
	req := &core.Request{Model: "gemini-2.5-flash"}
	stream := newStream(sse, req, nil)
	defer stream.Close()
	_, err := stream.Next()
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("err = %v, want quota message", err)
	}
}

func TestStreamPromptBlocked(t *testing.T) {
	sse := sseBody(
		`data: {"promptFeedback":{"blockReason":"SAFETY"}}`,
		`data: {"candidates":[{"finishReason":"SAFETY"}]}`,
	)
	req := &core.Request{Model: "gemini-2.5-flash"}
	stream := newStream(sse, req, nil)
	defer stream.Close()
	var done *core.DoneEvent
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if d, ok := event.(core.DoneEvent); ok {
			done = &d
		}
	}
	if done == nil || done.FinishReason != core.FinishReasonSafety || done.FinishReasonRaw != "SAFETY" {
		t.Fatalf("done = %+v", done)
	}
}

func TestStreamCleanCloseWithoutFinishReasonFails(t *testing.T) {
	sse := sseBody(`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	req := &core.Request{Model: "gemini-2.5-flash"}
	stream := newStream(sse, req, nil)
	defer stream.Close()
	for {
		_, err := stream.Next()
		if err == io.EOF {
			t.Fatalf("clean EOF without finish reason must be an incomplete-stream error")
		}
		if err != nil {
			if strings.Contains(err.Error(), "stream ended before a finish reason") {
				return
			}
			t.Fatalf("err = %v", err)
		}
	}
}

func TestStreamModelVersionUpdatesModel(t *testing.T) {
	sse := sseBody(
		`data: {"modelVersion":"gemini-2.5-flash","candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`,
		`data: {"candidates":[{"finishReason":"STOP"}]}`,
	)
	req := &core.Request{Model: "gemini-2.5-flash"}
	stream := newStream(sse, req, nil)
	defer stream.Close()
	var done *core.DoneEvent
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if d, ok := event.(core.DoneEvent); ok {
			done = &d
		}
	}
	if done == nil || done.Model != "gemini-2.5-flash" {
		t.Fatalf("done = %+v", done)
	}
}

func TestStreamSyntheticToolCallID(t *testing.T) {
	sse := sseBody(
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"q":1}}}]}}]}`,
		`data: {"candidates":[{"finishReason":"STOP"}]}`,
	)
	req := &core.Request{Model: "gemini-2.5-flash"}
	stream := newStream(sse, req, nil)
	defer stream.Close()
	var sawStart *core.ToolUseStart
	var sawDelta *core.ToolUseDelta
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch e := event.(type) {
		case core.ToolUseStart:
			sawStart = &e
		case core.ToolUseDelta:
			sawDelta = &e
		}
	}
	if sawStart == nil || sawStart.ID == "" || !strings.HasPrefix(sawStart.ID, "call_") {
		t.Fatalf("start = %+v, want synthesized call id", sawStart)
	}
	if sawDelta == nil || sawDelta.ArgumentsDelta == nil {
		t.Fatalf("delta = %+v, want full arguments", sawDelta)
	}
	var args map[string]any
	if err := json.Unmarshal(sawDelta.ArgumentsDelta, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["q"] != float64(1) {
		t.Fatalf("args = %+v", args)
	}
}
