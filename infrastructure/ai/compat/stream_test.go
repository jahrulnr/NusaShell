package compat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/internal/testgolden"
)

func TestStreamCumulativeReasoningAndToolDeltas(t *testing.T) {
	stream := streamFromSSE(t,
		testgolden.ReadFixtureString(t, "../../testdata/compat/minimax_stream.sse"),
		Spec{
			Name: "minimax",
			Stream: StreamSpec{
				ReasoningFields:     []string{"reasoning_content"},
				ReasoningCumulative: true,
				ContentCumulative:   true,
			},
		},
		&core.Request{Model: "minimax-text-01", Messages: []core.Message{core.UserText("hi")}},
	)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Reasoning() != "ab" || resp.Text() != "hi" {
		t.Fatalf("reasoning/text = %q/%q", resp.Reasoning(), resp.Text())
	}
	if len(resp.Blocks) == 0 {
		t.Fatalf("blocks is empty")
	}
	reasoning, ok := resp.Blocks[0].(core.ReasoningBlock)
	if !ok {
		t.Fatalf("first block = %T", resp.Blocks[0])
	}
	var details []map[string]any
	if err := json.Unmarshal(reasoning.Extra, &details); err != nil {
		t.Fatalf("reasoning extra: %v", err)
	}
	if len(details) != 1 || details[0]["text"] != "ab" {
		t.Fatalf("reasoning details = %#v", details)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
	if resp.Usage.InputTokens != 1 || resp.Usage.OutputTokens != 2 || resp.FinishReason != core.FinishReasonToolCall {
		t.Fatalf("usage/finish = %+v/%q", resp.Usage, resp.FinishReason)
	}
}

func TestStreamCumulativeContentRejectsRewrite(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"abc"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"ax"}}]}`,
		``,
	}, "\n"), Spec{Name: "minimax", Stream: StreamSpec{ContentCumulative: true}}, nil)
	_, err := core.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "cumulative content stream changed") {
		t.Fatalf("expected cumulative content error, got %v", err)
	}
}

func TestStreamCumulativeContentCanDependOnThinking(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"h"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"i"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{
		Name: "minimax",
		Stream: StreamSpec{
			ContentCumulative:          true,
			ContentCumulativeCondition: "thinking_enabled",
		},
		Request: RequestSpec{
			Thinking: func(*core.Thinking, string) (map[string]any, error) {
				return map[string]any{"thinking": map[string]any{"type": "disabled"}}, nil
			},
		},
	}, &core.Request{
		Model:    "m",
		Messages: []core.Message{core.UserText("hi")},
		Thinking: &core.Thinking{Mode: core.ThinkingDisabled},
	})
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "hi" {
		t.Fatalf("text = %q", resp.Text())
	}
}

func TestStreamConvertsRefusalAndCachedTokens(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"refusal":"no"}}]}`,
		`data: {"choices":[{"finish_reason":"content_filter"}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":6}}}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "strict"}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "no" || resp.Usage.CacheReadTokens != 6 || resp.FinishReason != core.FinishReasonSafety {
		t.Fatalf("response = text %q usage %+v finish %q", resp.Text(), resp.Usage, resp.FinishReason)
	}
}

func TestStreamPrependsStrictToolOmittedWarning(t *testing.T) {
	tool := mustTool(t, "lookup", "Lookup.", map[string]any{"type": "object"})
	tool.Strict = core.StrictEnabled
	stream := streamFromSSE(t, "data: [DONE]\n\n", Spec{
		Name:     "strictless",
		Features: FeatureSpec{StrictTools: StrictToolsOmit},
	}, &core.Request{Model: "m", Messages: []core.Message{core.UserText("hi")}, Tools: []core.Tool{tool}})
	defer stream.Close()
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	warning, ok := event.(core.WarningEvent)
	if !ok {
		t.Fatalf("first event = %#v, want WarningEvent", event)
	}
	if warning.Warning.Code != "request.strict_tool_omitted" || warning.Warning.Provider != "strictless" {
		t.Fatalf("warning = %#v", warning.Warning)
	}
}

func TestStreamCumulativeReasoningRejectsRewrite(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"abc"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"ax"}}]}`,
		``,
	}, "\n"), Spec{Name: "minimax", Stream: StreamSpec{ReasoningFields: []string{"reasoning_content"}, ReasoningCumulative: true}}, nil)
	_, err := core.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "cumulative reasoning stream changed") {
		t.Fatalf("expected cumulative reasoning error, got %v", err)
	}
}

// TestStreamMergesToolCallChunksIntoOneStart guards the OpenAI streaming
// protocol: id/name arrive only on the opening chunk, later chunks stream
// argument deltas without them. A backfilled id must not re-emit ToolUseStart,
// which downstream consumers turn into duplicate empty-named tool calls.
func TestStreamMergesToolCallChunksIntoOneStart(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "mimo"}, &core.Request{Model: "mimo-v2.5", Messages: []core.Message{core.UserText("hi")}})
	var starts []core.ToolUseStart
	var dones []core.ToolUseDone
	var args strings.Builder
	for {
		ev, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		switch e := ev.(type) {
		case core.ToolUseStart:
			starts = append(starts, e)
		case core.ToolUseDelta:
			args.Write(e.ArgumentsDelta)
		case core.ToolUseDone:
			dones = append(dones, e)
		}
	}
	if len(starts) != 1 {
		t.Fatalf("expected exactly 1 ToolUseStart, got %d: %+v", len(starts), starts)
	}
	if starts[0].ID != "call_1" || starts[0].Name != "lookup" {
		t.Fatalf("ToolUseStart = %+v", starts[0])
	}
	if args.String() != `{"q":"x"}` {
		t.Fatalf("aggregated arguments = %q", args.String())
	}
	if len(dones) != 1 || dones[0].ID != "call_1" {
		t.Fatalf("expected exactly 1 ToolUseDone for call_1, got %+v", dones)
	}
}

// TestStreamHoldsToolCallUntilLateName reproduces the gateway behavior behind
// ainovel-cli issue #75: the opening chunk carries only the id (arguments may
// even start streaming) and function.name arrives in a later chunk. The stream
// must defer ToolUseStart until the name is known — an early start with an
// empty name can never be corrected and downstream dispatch fails with
// `tool "" not found`.
func TestStreamHoldsToolCallUntilLateName(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"\"x\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls len = %d, want 1: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("call = %#v", calls[0])
	}
}

func TestStreamEmitsEarlyToolDeltaBeforeLateName(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"\"x\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	var sequence []string
	var deltas []core.ToolUseDelta
	resp, err := core.HandleWith(stream, core.StreamHandler{
		ToolStart: func(core.ToolUseStart) error {
			sequence = append(sequence, "start")
			return nil
		},
		ToolDelta: func(event core.ToolUseDelta) error {
			sequence = append(sequence, "delta")
			deltas = append(deltas, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("HandleWith returned error: %v", err)
	}
	if len(sequence) < 3 || sequence[0] != "delta" || sequence[1] != "start" || sequence[2] != "delta" {
		t.Fatalf("tool event sequence = %v, want delta, start, delta", sequence)
	}
	if len(deltas) != 2 || string(deltas[0].ArgumentsDelta) != `{"q":` || string(deltas[1].ArgumentsDelta) != `"x"}` {
		t.Fatalf("tool deltas = %#v, want two non-duplicated chunks", deltas)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("aggregated tool call = %#v", calls)
	}
}

// TestStreamIgnoresResentToolCallName guards against gateways that echo the
// full function.name on every argument chunk: the first non-empty name wins and
// later repeats must neither concatenate nor re-open the call.
func TestStreamIgnoresResentToolCallName(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"\"x\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls len = %d, want 1: %#v", len(calls), calls)
	}
	if calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("call = %#v", calls[0])
	}
}

// TestStreamFlushesNamelessToolCallOnFinish covers the pathological case where
// the name never arrives at all: finish_reason must flush the buffered call
// (empty name and all) so the consumer sees a visible dispatch error instead of
// the call silently vanishing.
func TestStreamFlushesNamelessToolCallOnFinish(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	var starts []core.ToolUseStart
	var dones []core.ToolUseDone
	var args strings.Builder
	for {
		ev, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		switch e := ev.(type) {
		case core.ToolUseStart:
			starts = append(starts, e)
		case core.ToolUseDelta:
			args.Write(e.ArgumentsDelta)
		case core.ToolUseDone:
			dones = append(dones, e)
		}
	}
	if len(starts) != 1 || starts[0].ID != "call_1" || starts[0].Name != "" {
		t.Fatalf("starts = %+v, want 1 start with id call_1 and empty name", starts)
	}
	if args.String() != `{}` {
		t.Fatalf("aggregated arguments = %q", args.String())
	}
	if len(dones) != 1 || dones[0].ID != "call_1" {
		t.Fatalf("dones = %+v", dones)
	}
}

// TestStreamClosesArglessToolCall reproduces an argument-less tool call: the
// model opens a call and finishes without streaming any arguments. The stream
// must still emit ToolUseDone so the consumer can finalize the call.
func TestStreamClosesArglessToolCall(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"novel_context"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "mimo"}, &core.Request{Model: "mimo-v2.5", Messages: []core.Message{core.UserText("hi")}})
	var starts, dones int
	for {
		ev, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		switch e := ev.(type) {
		case core.ToolUseStart:
			starts++
		case core.ToolUseDone:
			if e.ID != "call_9" {
				t.Fatalf("ToolUseDone id = %q", e.ID)
			}
			dones++
		}
	}
	if starts != 1 || dones != 1 {
		t.Fatalf("expected 1 start and 1 done, got start=%d done=%d", starts, dones)
	}
}

func TestStreamSeparatesToolCallsByChoiceIndex(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"first","arguments":"{\"a\":1}"}}]}},{"index":1,"delta":{"tool_calls":[{"index":0,"id":"call_b","function":{"name":"second","arguments":"{\"b\":2}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[{"index":1,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("tool calls len = %d, want 2: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_a" || calls[0].Name != "first" || string(calls[0].Arguments) != `{"a":1}` {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].ID != "call_b" || calls[1].Name != "second" || string(calls[1].Arguments) != `{"b":2}` {
		t.Fatalf("second call = %#v", calls[1])
	}
}

func TestStreamRejectsMalformedToolCall(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "non_object",
			body: `data: {"choices":[{"index":0,"delta":{"tool_calls":["bad"]}}]}`,
			want: "tool_call must be an object",
		},
		{
			name: "non_string_arguments",
			body: `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":123}}]}}]}`,
			want: "arguments must be string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := streamFromSSE(t, tt.body, Spec{Name: "strict"}, nil)
			_, err := stream.Next()
			if err == nil || !strings.Contains(err.Error(), tt.want) || !core.IsProviderError(err) {
				t.Fatalf("expected malformed tool call provider error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestStreamCleanEOFWithoutDoneSentinel(t *testing.T) {
	// Many OpenAI-compatible hosts (OpenCode/Zen, TokenRouter, local
	// gateways) close the connection after the last chunk without sending
	// the [DONE] sentinel, but they do send finish_reason in the last
	// chunk. A clean EOF with a finish_reason is a normal end of stream;
	// the accumulated content must still be delivered.
	stream := streamFromSSE(t, `data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"stop"}]}`, Spec{Name: "strict"}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("clean EOF with finish_reason must not error, got %v", err)
	}
	if resp.Text() != "partial" {
		t.Fatalf("content = %q, want partial", resp.Text())
	}
}

func TestStreamCleanEOFWithoutFinishReasonErrors(t *testing.T) {
	// A stream that ends without either a [DONE] sentinel or a
	// finish_reason is a mid-stream cut (transient failure), not a normal
	// end. Surface it so the retry loop can reconnect.
	stream := streamFromSSE(t, `data: {"choices":[{"delta":{"content":"partial"}}]}`, Spec{Name: "strict"}, nil)
	_, err := core.Collect(stream)
	if err == nil || !strings.Contains(err.Error(), "without finish_reason") || !core.IsNetworkError(err) {
		t.Fatalf("expected incomplete stream error, got %v", err)
	}
}

// TestStreamEmitsReasoningBeforeTextInCombinedDelta proves that a single
// SSE delta carrying both reasoning and content (seen at the
// reasoning→answer transition with GLM via NVIDIA NIM, DeepSeek via
// OpenRouter, etc.) must emit the reasoning chunk before the text chunk.
// Reasoning always precedes the answer, so the EventCollector must merge
// all reasoning into one block and all text into one block — not
// interleave them. Emitting text before reasoning in a combined delta
// fragments the blocks: ReasoningBlock, TextBlock, ReasoningBlock,
// TextBlock instead of ReasoningBlock, TextBlock.
//
// Mirrors zendev-sh/goai PR #119.
func TestStreamEmitsReasoningBeforeTextInCombinedDelta(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"existing aesthetic"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":".","content":"V"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"oy a explorar"}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n"), Spec{
		Name: "compat",
		Stream: StreamSpec{
			ReasoningFields: []string{"reasoning_content"},
		},
	}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if len(resp.Blocks) != 2 {
		t.Fatalf("expected exactly 2 blocks (ReasoningBlock + TextBlock), got %d: %#v", len(resp.Blocks), resp.Blocks)
	}
	if _, ok := resp.Blocks[0].(core.ReasoningBlock); !ok {
		t.Fatalf("first block must be ReasoningBlock, got %T: %#v", resp.Blocks[0], resp.Blocks[0])
	}
	if _, ok := resp.Blocks[1].(core.TextBlock); !ok {
		t.Fatalf("second block must be TextBlock, got %T: %#v", resp.Blocks[1], resp.Blocks[1])
	}
	if resp.Reasoning() != "existing aesthetic." {
		t.Fatalf("reasoning = %q, want %q", resp.Reasoning(), "existing aesthetic.")
	}
	if resp.Text() != "Voy a explorar" {
		t.Fatalf("text = %q, want %q", resp.Text(), "Voy a explorar")
	}
}

// TestStreamCompletesWithoutDoneSentinelOnOpenConnection reproduces the
// gateway behavior behind semantic-first completion: the server sends the
// final chunk carrying finish_reason and then keeps the connection open
// without ever writing the [DONE] trailer. The stream must complete from
// finish_reason — not stall until the idle watchdog, and not fall into a
// retry that would re-run a turn the model already finished.
func TestStreamCompletesWithoutDoneSentinelOnOpenConnection(t *testing.T) {
	stream, writer := pipeStream(t, Spec{Name: "compat"}, nil)
	stream.drainWindow = 50 * time.Millisecond
	go func() {
		io.WriteString(writer, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
	}()
	safety := time.AfterFunc(5*time.Second, func() { writer.Close() })
	defer safety.Stop()

	start := time.Now()
	resp, err := core.Collect(stream)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "done" || resp.FinishReason != core.FinishReasonStop {
		t.Fatalf("text/finish = %q/%q", resp.Text(), resp.FinishReason)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("stream took %v to complete without [DONE]; finish_reason must complete the stream", elapsed)
	}
}

// TestStreamCapturesUsageAfterFinishWithoutDoneSentinel proves the final
// accounting chunk that OpenAI-compatible providers emit after finish_reason
// is still captured (usage must never be required for success, but it must
// not be thrown away either) and that it terminates the stream on its own:
// the [DONE] trailer is not required.
func TestStreamCapturesUsageAfterFinishWithoutDoneSentinel(t *testing.T) {
	stream, writer := pipeStream(t, Spec{Name: "compat"}, nil)
	go func() {
		io.WriteString(writer, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		io.WriteString(writer, `data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`+"\n\n")
	}()
	safety := time.AfterFunc(5*time.Second, func() { writer.Close() })
	defer safety.Stop()

	start := time.Now()
	resp, err := core.Collect(stream)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %+v, want 3/4", resp.Usage)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("stream waited %v after the accounting chunk; the usage chunk must complete it", elapsed)
	}
}

// TestStreamPostFinishConnectionErrorCompletes covers a connection reset that
// arrives after finish_reason: the model already reported completion, so the
// turn is not partial and must not surface as a retryable stream error.
func TestStreamPostFinishConnectionErrorCompletes(t *testing.T) {
	stream, writer := pipeStream(t, Spec{Name: "compat"}, nil)
	go func() {
		io.WriteString(writer, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		writer.CloseWithError(errors.New("connection reset by peer"))
	}()

	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("post-finish connection error must not fail the turn: %v", err)
	}
	if resp.Text() != "done" || resp.FinishReason != core.FinishReasonStop {
		t.Fatalf("text/finish = %q/%q", resp.Text(), resp.FinishReason)
	}
}

// TestStreamPayloadErrorIsFailure guards streams that report an error inside
// an HTTP 200 SSE body. Providers use both the object form
// {"error":{"message":...}} and the string form {"error":"..."}; either way
// the chunk is a failure, not an empty successful response.
func TestStreamPayloadErrorIsFailure(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "object",
			body: `data: {"error":{"message":"boom","type":"server_error"}}`,
		},
		{
			name: "string",
			body: `data: {"error":"boom"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := streamFromSSE(t, strings.Join([]string{tt.body, `data: [DONE]`, ``}, "\n"), Spec{Name: "compat"}, nil)
			_, err := core.Collect(stream)
			if err == nil || !strings.Contains(err.Error(), "boom") || !core.IsProviderError(err) {
				t.Fatalf("expected provider error mentioning boom, got %v", err)
			}
		})
	}
}

// TestStreamFinishReasonErrorIsFailure guards the one finish_reason that is
// not a successful completion: a provider reporting "error" (or any of its
// aliases) must fail the turn instead of producing an empty successful
// response.
func TestStreamFinishReasonErrorIsFailure(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"error"}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	_, err := core.Collect(stream)
	if err == nil || !core.IsProviderError(err) {
		t.Fatalf("finish_reason=error must fail, got %v", err)
	}
}

// TestStreamIgnoresMalformedChunkAfterFinish keeps trailing noise from
// failing a turn that already completed: after finish_reason the response is
// complete, so a malformed trailing frame is skipped instead of surfacing as
// a provider error.
func TestStreamIgnoresMalformedChunkAfterFinish(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`,
		`data: {not valid json`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("malformed trailing frame must not fail the stream: %v", err)
	}
	if resp.Text() != "done" {
		t.Fatalf("text = %q, want done", resp.Text())
	}
}

// TestStreamDoneSentinelWithoutFinishReasonCompletes characterizes the
// [DONE]-only path: a provider may close out with the trailer without ever
// sending finish_reason, and that still completes the stream.
func TestStreamDoneSentinelWithoutFinishReasonCompletes(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	resp, err := core.Collect(stream)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if resp.Text() != "hi" {
		t.Fatalf("text = %q, want hi", resp.Text())
	}
}

// TestStreamRepeatedFinishReasonOnUsageChunkCompletesOnce covers OpenRouter's
// accounting frame: it repeats finish_reason on the usage chunk and asks
// clients to treat that as accounting, not as a second terminal. The stream
// must complete exactly once, keep the usage, and not emit a second
// DoneEvent.
func TestStreamRepeatedFinishReasonOnUsageChunkCompletesOnce(t *testing.T) {
	stream := streamFromSSE(t, strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
		`data: [DONE]`,
		``,
	}, "\n"), Spec{Name: "compat"}, nil)
	var dones int
	var usage core.Usage
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		switch e := event.(type) {
		case core.DoneEvent:
			dones++
			if e.FinishReason != core.FinishReasonStop {
				t.Fatalf("DoneEvent finish reason = %q", e.FinishReason)
			}
		case core.UsageEvent:
			usage = e.Usage
		}
	}
	if dones != 1 {
		t.Fatalf("DoneEvent count = %d, want exactly 1", dones)
	}
	if usage.InputTokens != 1 || usage.OutputTokens != 2 {
		t.Fatalf("usage = %+v, want 1/2", usage)
	}
}

// pipeStream builds a stream whose body is fully test-controlled: the caller
// writes SSE frames through the returned writer and decides when (or whether)
// the connection ends.
func pipeStream(t *testing.T, spec Spec, req *core.Request) (*stream, *io.PipeWriter) {
	t.Helper()
	reader, writer := io.Pipe()
	if req == nil {
		req = &core.Request{Model: "m", Messages: []core.Message{core.UserText("hi")}}
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader}
	resp.Header.Set("Content-Type", "text/event-stream")
	return newStream(resp, req, spec), writer
}

func streamFromSSE(t *testing.T, body string, spec Spec, req *core.Request) core.Stream {
	t.Helper()
	provider, err := New(Config{
		BaseURL: "https://compat.example/v1",
		HTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return streamResponse(body), nil
		}),
	}, spec)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if req == nil {
		req = &core.Request{Model: "m", Messages: []core.Message{core.UserText("hi")}}
	}
	stream, err := provider.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	return stream
}

func streamResponse(body string) *http.Response {
	resp := jsonResponse(http.StatusOK, body)
	resp.Header.Set("Content-Type", "text/event-stream")
	return resp
}
