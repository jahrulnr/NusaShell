package codex

import (
	"encoding/json"
	"fmt"
	"io"

	"nusashell/infrastructure/ai/core"
)

type providerStream struct {
	raw     *ResponsesStream
	model   string
	pending []core.Event
	done    bool

	toolIDs     map[string]string
	toolStarted map[string]bool
	toolArgs    map[string]bool
	toolSeen    bool

	lastReasoningItemID       string
	lastReasoningSummaryIndex *int
}

type compactionProviderStream struct {
	raw      *ResponsesStream
	model    string
	open     func() (*ResponsesStream, error)
	maxRetry int
	retries  int
	pending  []core.Event
	done     bool
}

func (s *compactionProviderStream) Next() (core.Event, error) {
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		return event, nil
	}
	if s.done {
		return nil, io.EOF
	}
	var output *CompactionOutput
	for {
		var err error
		output, err = CollectCompactionOutput(s.raw)
		if err == nil {
			break
		}
		if !IsRetryableStreamError(err) || s.retries >= s.maxRetry || s.open == nil {
			return nil, err
		}
		s.retries++
		_ = s.raw.Close()
		s.raw, err = s.open()
		if err != nil {
			if !IsRetryableStreamError(err) || s.retries >= s.maxRetry {
				return nil, err
			}
			continue
		}
	}
	raw := output.CompactionItem.Raw
	if len(raw) == 0 {
		raw, _ = json.Marshal(output.CompactionItem)
	}
	if output.TokenUsage != nil {
		s.pending = append(s.pending, core.UsageEvent{Usage: codexUsage(output.TokenUsage, s.model)})
	}
	s.pending = append(s.pending,
		core.ProviderEvent{Name: "compaction", Raw: append(json.RawMessage(nil), raw...)},
		core.DoneEvent{FinishReason: core.FinishReasonStop, Provider: "codex", Model: s.model},
	)
	s.done = true
	return s.Next()
}

func (s *compactionProviderStream) Close() error {
	if s.raw == nil {
		return nil
	}
	return s.raw.Close()
}

func (s *providerStream) Next() (core.Event, error) {
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		return event, nil
	}
	if s.done {
		return nil, io.EOF
	}
	if s.toolIDs == nil {
		s.toolIDs = make(map[string]string)
		s.toolStarted = make(map[string]bool)
		s.toolArgs = make(map[string]bool)
	}

	event, err := s.raw.Next()
	if err != nil {
		return nil, err
	}
	switch value := event.(type) {
	case EventCreated:
		return s.next()
	case EventOutputItemAdded:
		if value.Item.Type != ItemTypeFunctionCall {
			return s.next()
		}
		id := responseItemToolID(value.Item)
		s.toolIDs[value.Item.ID] = id
		s.toolStarted[value.Item.ID] = true
		s.toolSeen = true
		return core.ToolUseStart{
			ID:     id,
			Name:   value.Item.Name,
			ItemID: value.Item.ID,
		}, nil
	case EventOutputItemDone:
		return s.outputItemDone(value.Item)
	case EventCompleted:
		s.done = true
		var events []core.Event
		if value.TokenUsage != nil {
			events = append(events, core.UsageEvent{Usage: codexUsage(value.TokenUsage, s.model)})
		}
		finish := core.FinishReasonStop
		if s.toolSeen {
			finish = core.FinishReasonToolCall
		}
		events = append(events, core.DoneEvent{
			FinishReason: finish,
			Provider:     "codex",
			Model:        s.model,
		})
		s.pending = events
		return s.next()
	case EventError:
		return core.ErrorEvent{Err: value.Err}, nil
	case EventOther:
		return s.other(value)
	default:
		return s.next()
	}
}

func (s *providerStream) next() (core.Event, error) {
	return s.Next()
}

func (s *providerStream) outputItemDone(item ResponseItem) (core.Event, error) {
	switch item.Type {
	case ItemTypeCompaction:
		raw := item.Raw
		if len(raw) == 0 {
			raw, _ = json.Marshal(item)
		}
		return core.ProviderEvent{Name: "compaction", Raw: append(json.RawMessage(nil), raw...)}, nil
	case ItemTypeReasoning:
		// The visible reasoning text already streamed incrementally via
		// response.reasoning_text.delta / response.reasoning_summary_text.delta
		// events. Re-emitting the full summary here duplicated the whole
		// thinking block in the UI and persisted transcript (the same text was
		// appended again on output_item.done). The done item only contributes
		// opaque Extra (encrypted_content / wire item) for replay, mirroring
		// the OpenAI Responses path.
		raw := item.Raw
		if len(raw) == 0 {
			raw, _ = json.Marshal(item)
		}
		return core.ReasoningDelta{
			Summary:   len(item.Summary) > 0,
			Extra:     append(json.RawMessage(nil), raw...),
			ExtraFull: true,
		}, nil
	case ItemTypeFunctionCall:
		id := responseItemToolID(item)
		if !s.toolStarted[item.ID] {
			s.toolStarted[item.ID] = true
			s.toolIDs[item.ID] = id
			s.toolSeen = true
			events := []core.Event{core.ToolUseStart{ID: id, Name: item.Name, ItemID: item.ID}}
			if item.Arguments != "" {
				events = append(events, core.ToolUseDelta{
					ID:             id,
					ItemID:         item.ID,
					ArgumentsDelta: []byte(item.Arguments),
				})
			}
			events = append(events, core.ToolUseDone{ID: id, ItemID: item.ID})
			s.pending = events[1:]
			return events[0], nil
		}
		if item.Arguments != "" && !s.toolArgs[item.ID] {
			s.toolArgs[item.ID] = true
			s.pending = append(s.pending, core.ToolUseDone{ID: id, ItemID: item.ID})
			return core.ToolUseDelta{
				ID:             id,
				ItemID:         item.ID,
				ArgumentsDelta: []byte(item.Arguments),
			}, nil
		}
		return core.ToolUseDone{ID: id, ItemID: item.ID}, nil
	default:
		return s.next()
	}
}

func (s *providerStream) other(event EventOther) (core.Event, error) {
	switch event.Type {
	case "response.output_text.delta":
		var delta struct {
			Text         string `json:"delta"`
			OutputIndex  *int   `json:"output_index,omitempty"`
			ContentIndex *int   `json:"content_index,omitempty"`
		}
		if err := json.Unmarshal(event.Raw, &delta); err != nil {
			return nil, fmt.Errorf("codex: decode output text delta: %w", err)
		}
		return core.ContentDelta{Text: delta.Text, OutputIndex: delta.OutputIndex, ContentIndex: delta.ContentIndex}, nil
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		var delta struct {
			Text         string `json:"delta"`
			ItemID       string `json:"item_id,omitempty"`
			SummaryIndex *int   `json:"summary_index,omitempty"`
		}
		if err := json.Unmarshal(event.Raw, &delta); err != nil {
			return nil, fmt.Errorf("codex: decode reasoning delta: %w", err)
		}
		text := delta.Text
		if event.Type == "response.reasoning_summary_text.delta" && text != "" && s.reasoningSummaryChanged(delta.ItemID, delta.SummaryIndex) {
			text = "\n\n" + text
		}
		return core.ReasoningDelta{Text: text, Summary: event.Type == "response.reasoning_summary_text.delta"}, nil
	case "response.function_call_arguments.delta":
		var delta struct {
			Text        string `json:"delta"`
			ItemID      string `json:"item_id"`
			OutputIndex *int   `json:"output_index,omitempty"`
		}
		if err := json.Unmarshal(event.Raw, &delta); err != nil {
			return nil, fmt.Errorf("codex: decode function arguments delta: %w", err)
		}
		id := s.toolIDs[delta.ItemID]
		if id == "" {
			id = delta.ItemID
			s.toolIDs[delta.ItemID] = id
		}
		s.toolArgs[delta.ItemID] = true
		s.toolSeen = true
		return core.ToolUseDelta{
			ID:             id,
			ItemID:         delta.ItemID,
			OutputIndex:    delta.OutputIndex,
			ArgumentsDelta: []byte(delta.Text),
		}, nil
	case "response.function_call_arguments.done":
		var done struct {
			Name        string `json:"name"`
			Arguments   string `json:"arguments"`
			ItemID      string `json:"item_id"`
			OutputIndex *int   `json:"output_index,omitempty"`
		}
		if err := json.Unmarshal(event.Raw, &done); err != nil {
			return nil, fmt.Errorf("codex: decode function arguments done: %w", err)
		}
		id := s.toolIDs[done.ItemID]
		if id == "" {
			id = done.ItemID
			s.toolIDs[done.ItemID] = id
		}
		s.toolSeen = true
		if !s.toolStarted[done.ItemID] {
			s.toolStarted[done.ItemID] = true
			events := []core.Event{core.ToolUseStart{ID: id, Name: done.Name, ItemID: done.ItemID, OutputIndex: done.OutputIndex}}
			if done.Arguments != "" {
				events = append(events, core.ToolUseDelta{ID: id, ItemID: done.ItemID, OutputIndex: done.OutputIndex, ArgumentsDelta: []byte(done.Arguments)})
			}
			events = append(events, core.ToolUseDone{ID: id, ItemID: done.ItemID, OutputIndex: done.OutputIndex})
			s.pending = events[1:]
			return events[0], nil
		}
		return core.ToolUseDone{ID: id, ItemID: done.ItemID, OutputIndex: done.OutputIndex}, nil
	case "response.output_text.done", "response.reasoning_text.done",
		"response.reasoning_summary_text.done", "response.in_progress",
		"response.queued", "response.content_part.added",
		"response.content_part.done":
		return s.next()
	default:
		return core.ProviderEvent{Name: event.Type, Raw: append(json.RawMessage(nil), event.Raw...)}, nil
	}
}

func (s *providerStream) Close() error {
	if s.raw == nil {
		return nil
	}
	return s.raw.Close()
}

func (s *providerStream) reasoningSummaryChanged(itemID string, summaryIndex *int) bool {
	changed := false
	if itemID != "" && s.lastReasoningItemID != "" && itemID != s.lastReasoningItemID {
		changed = true
	}
	if summaryIndex != nil && s.lastReasoningSummaryIndex != nil && *summaryIndex != *s.lastReasoningSummaryIndex {
		changed = true
	}
	if itemID != "" {
		s.lastReasoningItemID = itemID
	}
	if summaryIndex != nil {
		index := *summaryIndex
		s.lastReasoningSummaryIndex = &index
	}
	return changed
}

func responseItemToolID(item ResponseItem) string {
	if item.CallID != "" {
		return item.CallID
	}
	return item.ID
}

func codexUsage(usage *TokenUsage, model string) core.Usage {
	if usage == nil {
		return core.Usage{}
	}
	cached := boundedInt(usage.CachedInputTokens)
	input := boundedInt(usage.InputTokens) - cached
	if input < 0 {
		input = 0
	}
	return core.Usage{
		InputTokens:      input,
		OutputTokens:     boundedInt(usage.OutputTokens),
		TotalTokens:      boundedInt(usage.TotalTokens),
		ReasoningTokens:  boundedInt(usage.ReasoningOutputTokens),
		CacheReadTokens:  cached,
		CacheWriteTokens: boundedInt(usage.CacheWriteInputTokens),
		Provider:         "codex",
		Model:            model,
	}
}

func boundedInt(value int64) int {
	if value <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}
