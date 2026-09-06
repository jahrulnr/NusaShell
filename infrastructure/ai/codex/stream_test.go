package codex

import (
	"errors"
	"io"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

// sseBody joins SSE frames into a stream body.
func sseBody(frames ...string) io.Reader {
	return strings.NewReader(strings.Join(frames, "\n\n") + "\n\n")
}

func collectEvents(t *testing.T, body io.Reader) []ResponseEvent {
	t.Helper()
	stream := NewResponsesStream(body)
	var events []ResponseEvent
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		events = append(events, event)
	}
}

func TestResponsesStreamDecodesCompactionFlow(t *testing.T) {
	events := collectEvents(t, sseBody(
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}",
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}",
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"cmp_001\",\"type\":\"compaction\",\"encrypted_content\":\"ENC-STREAM\",\"summary\":[]}}",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":100,\"input_tokens_details\":{\"cached_tokens\":40,\"cache_write_tokens\":10},\"output_tokens\":50,\"output_tokens_details\":{\"reasoning_tokens\":30},\"total_tokens\":150}}}",
	))
	if len(events) != 4 {
		t.Fatalf("events len = %d, want 4", len(events))
	}
	created, ok := events[0].(EventCreated)
	if !ok || created.ResponseID != "resp_1" {
		t.Fatalf("events[0] = %#v, want EventCreated resp_1", events[0])
	}
	added, ok := events[1].(EventOutputItemAdded)
	if !ok || added.Item.Type != ItemTypeMessage {
		t.Fatalf("events[1] = %#v, want EventOutputItemAdded message", events[1])
	}
	done, ok := events[2].(EventOutputItemDone)
	if !ok || !done.Item.IsCompaction() || done.Item.EncryptedContent != "ENC-STREAM" {
		t.Fatalf("events[2] = %#v, want EventOutputItemDone compaction", events[2])
	}
	completed, ok := events[3].(EventCompleted)
	if !ok || completed.ResponseID != "resp_1" {
		t.Fatalf("events[3] = %#v, want EventCompleted resp_1", events[3])
	}
	if completed.TokenUsage == nil {
		t.Fatalf("completed usage missing")
	}
	usage := completed.TokenUsage
	if usage.InputTokens != 100 || usage.OutputTokens != 50 || usage.TotalTokens != 150 ||
		usage.CachedInputTokens != 40 || usage.CacheWriteInputTokens != 10 || usage.ReasoningOutputTokens != 30 {
		t.Fatalf("usage = %+v, want mapped TokenUsage", usage)
	}
	if len(completed.UsageMetadata) == 0 || !strings.Contains(string(completed.UsageMetadata), "input_tokens") {
		t.Fatalf("usage metadata = %s, want raw usage passthrough", completed.UsageMetadata)
	}
}

func TestResponsesStreamCompletedWithoutUsage(t *testing.T) {
	// usage is optional: accounting then relies on stale data and local
	// estimates, and the completion is still valid.
	events := collectEvents(t, sseBody(
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_9\"}}",
	))
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	completed, ok := events[0].(EventCompleted)
	if !ok {
		t.Fatalf("events[0] = %#v, want EventCompleted", events[0])
	}
	if completed.TokenUsage != nil {
		t.Fatalf("usage = %+v, want nil", completed.TokenUsage)
	}
}

func TestResponsesStreamClosedBeforeCompletedIsStreamError(t *testing.T) {
	stream := NewResponsesStream(sseBody(
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}",
	))
	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	_, err := stream.Next()
	if err == nil {
		t.Fatalf("second Next = nil error, want stream error")
	}
	if !strings.Contains(err.Error(), "stream closed before response.completed") {
		t.Fatalf("err = %v, want stream-closed-before-completed", err)
	}
	if !core.IsNetworkError(err) || !core.IsRetryableError(err) {
		t.Fatalf("err = %v, want retryable network error", err)
	}
}

func TestResponsesStreamFailedResponseIsErrorEvent(t *testing.T) {
	stream := NewResponsesStream(sseBody(
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}}",
	))
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	failed, ok := event.(EventError)
	if !ok {
		t.Fatalf("event = %#v, want EventError", event)
	}
	if !strings.Contains(failed.Err.Error(), "boom") || !strings.Contains(failed.Err.Error(), "server_error") {
		t.Fatalf("err = %v, want coded provider error", failed.Err)
	}
	// Terminal: the next read is EOF.
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after failed = %v, want io.EOF", err)
	}
}

func TestResponsesStreamInfersEventTypeFromData(t *testing.T) {
	// Some providers omit the event: line; the data payload's type field
	// names the event.
	events := collectEvents(t, sseBody(
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\"}}",
	))
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if completed, ok := events[0].(EventCompleted); !ok || completed.ResponseID != "resp_2" {
		t.Fatalf("events[0] = %#v, want EventCompleted resp_2", events[0])
	}
}

func TestResponsesStreamSurfacesUnknownEventsAsOther(t *testing.T) {
	events := collectEvents(t, sseBody(
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}",
		"event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_3\"}}",
	))
	if len(events) != 3 {
		t.Fatalf("events len = %d, want 3", len(events))
	}
	other, ok := events[0].(EventOther)
	if !ok || other.Type != "response.output_text.delta" || !strings.Contains(string(other.Raw), "hi") {
		t.Fatalf("events[0] = %#v, want EventOther output_text.delta", events[0])
	}
	if _, ok := events[1].(EventOther); !ok {
		t.Fatalf("events[1] = %#v, want EventOther", events[1])
	}
}

func TestResponsesStreamEOFAfterCompleted(t *testing.T) {
	stream := NewResponsesStream(sseBody(
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_4\"}}",
	))
	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after completed = %v, want io.EOF", err)
	}
}
