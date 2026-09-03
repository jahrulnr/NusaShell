package events

import (
	"testing"

	"nusashell-pets/internal/state"
)

func TestDecodeMapsNusaShellLifecycleEvents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  state.Event
	}{
		{
			name:  "turn started",
			input: `{"type":"agent.turn.started","payload":{"run_id":"run-1"}}`,
			want:  state.Event{State: state.StateThinking},
		},
		{
			name:  "tool started",
			input: `{"type":"agent.tool.started","payload":{"name":"docs"}}`,
			want:  state.Event{State: state.StateReasoning, Title: "Executing…", Message: "docs(...)"},
		},
		{
			name:  "tool completed",
			input: `{"type":"agent.tool.completed","payload":{"name":"docs"}}`,
			want:  state.Event{State: state.StateThinking},
		},
		{
			name:  "compacting",
			input: `{"type":"agent.compacting","payload":{}}`,
			want:  state.Event{State: state.StateReasoning, Title: "Compacting…", Message: "Making room for the next step…"},
		},
		{
			name:  "compacted",
			input: `{"type":"agent.compacted","payload":{"summary":"ok"}}`,
			want:  state.Event{State: state.StateThinking},
		},
		{
			name:  "turn done",
			input: `{"type":"agent.turn.done","payload":{}}`,
			want:  state.Event{State: state.StateDone},
		},
		{
			name:  "turn error",
			input: `{"type":"agent.turn.error","payload":{"message":"provider failed"}}`,
			want:  state.Event{State: state.StateError, Message: "provider failed"},
		},
		{
			name:  "ask pending",
			input: `{"type":"agent.ask.pending","payload":{"question":"choose one"}}`,
			want:  state.Event{State: state.StateWaiting, Message: "choose one"},
		},
		{
			name:  "compaction failed resumes thinking",
			input: `{"type":"agent.compaction.failed","payload":{"error":"not enough room"}}`,
			want:  state.Event{State: state.StateThinking, Message: "not enough room"},
		},
		{
			name:  "provider retry",
			input: `{"type":"agent.provider.retry","payload":{"error":"temporary"}}`,
			want:  state.Event{State: state.StateReasoning, Title: "Retrying…", Message: "temporary"},
		},
		{
			name:  "steer cancelled",
			input: `{"type":"agent.steer.cancelled","payload":{"reason":"discarded"}}`,
			want:  state.Event{State: state.StateThinking},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := Decode([]byte(tc.input))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !ok {
				t.Fatal("Decode reported event as ignored")
			}
			if got != tc.want {
				t.Fatalf("event = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecodeIgnoresUnrelatedEvents(t *testing.T) {
	t.Parallel()
	got, ok, err := Decode([]byte(`{"type":"agent.message.delta","payload":{"text":"hi"}}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if ok || got != (state.Event{}) {
		t.Fatalf("unrelated event = %+v, ok=%v", got, ok)
	}
}

func TestDecodeRejectsMalformedEnvelope(t *testing.T) {
	t.Parallel()
	if _, _, err := Decode([]byte(`{"type":`)); err == nil {
		t.Fatal("expected malformed envelope error")
	}
}
