package application

import (
	"fmt"
	"io"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// stubStream is a core.Stream that yields events from a pre-built response
// then ends with a DoneEvent.
type stubStream struct {
	events []core.Event
	idx    int
}

func (s *stubStream) Next() (core.Event, error) {
	if s.idx >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *stubStream) Close() error { return nil }

type eventsThenErrorStream struct {
	events   []core.Event
	err      error
	idx      int
	failOnce bool
}

func (s *eventsThenErrorStream) Next() (core.Event, error) {
	if s.idx < len(s.events) {
		event := s.events[s.idx]
		s.idx++
		return event, nil
	}
	if !s.failOnce {
		s.failOnce = true
		return nil, s.err
	}
	return nil, io.EOF
}

func (s *eventsThenErrorStream) Close() error { return nil }

// stubProviderContext wraps a core.Provider in a ProviderContext for tests.
func stubProviderContext(p core.Provider) ProviderContext {
	return ProviderContext{Provider: p, Kind: domain.ProviderChat}
}

func chatResponseToCore(r ChatResponse) *core.Response {
	resp := &core.Response{FinishReason: core.FinishReasonStop}
	if r.Content != "" {
		resp.Blocks = append(resp.Blocks, core.TextBlock{Text: r.Content})
	}
	if r.Reasoning != "" {
		resp.Blocks = append(resp.Blocks, core.ReasoningBlock{Text: r.Reasoning})
	}
	for _, tc := range r.ToolCalls {
		resp.Blocks = append(resp.Blocks, core.ToolUseBlock{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: jsonRaw(tc.Args),
		})
		resp.FinishReason = core.FinishReasonToolCall
	}
	return resp
}

func coreResponseEvents(resp *core.Response) []core.Event {
	var events []core.Event
	for _, b := range resp.Blocks {
		switch v := b.(type) {
		case core.TextBlock:
			events = append(events, core.ContentDelta{Text: v.Text})
		case core.ReasoningBlock:
			events = append(events, core.ReasoningDelta{Text: v.Text})
		case core.ToolUseBlock:
			idx := 0
			id := v.ID
			if id == "" {
				id = fmt.Sprintf("tool_%d", len(events))
			}
			events = append(events, core.ToolUseStart{ID: id, Name: v.Name, Index: &idx})
			events = append(events, core.ToolUseDelta{ID: id, Index: &idx, ArgumentsDelta: v.Arguments})
			events = append(events, core.ToolUseDone{ID: id, Index: &idx})
		}
	}
	events = append(events, core.DoneEvent{FinishReason: resp.FinishReason, Provider: "test-stub", Model: "test-model"})
	return events
}
