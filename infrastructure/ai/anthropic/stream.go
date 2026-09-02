package anthropic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nusashell/infrastructure/ai/core"
)

type stream struct {
	resp             *http.Response
	scanner          *bufio.Scanner
	includeReasoning bool
	pending          []core.Event
	done             bool
	model            string
	usage            core.Usage
	finish           core.FinishReason
	toolIDs          map[int]string
	toolNames        map[int]string
}

type streamChunk struct {
	Type         string          `json:"type"`
	Index        int             `json:"index,omitempty"`
	Delta        *streamDelta    `json:"delta,omitempty"`
	Usage        *anthropicUsage `json:"usage,omitempty"`
	Message      *streamMessage  `json:"message,omitempty"`
	Error        *streamError    `json:"error,omitempty"`
	ContentBlock *struct {
		Type      string         `json:"type"`
		ID        string         `json:"id,omitempty"`
		Name      string         `json:"name,omitempty"`
		Input     map[string]any `json:"input,omitempty"`
		Thinking  string         `json:"thinking,omitempty"`
		Signature string         `json:"signature,omitempty"`
		Data      string         `json:"data,omitempty"`
	} `json:"content_block,omitempty"`
}

type streamMessage struct {
	ID    string          `json:"id,omitempty"`
	Model string          `json:"model,omitempty"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type streamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type streamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

func newStream(resp *http.Response, req *core.Request, warnings []core.Warning) *stream {
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	s := &stream{
		resp:             resp,
		scanner:          scanner,
		includeReasoning: req == nil || req.Thinking == nil || req.Thinking.Mode != core.ThinkingDisabled,
		model:            req.Model,
		toolIDs:          make(map[int]string),
		toolNames:        make(map[int]string),
	}
	for _, w := range warnings {
		s.pending = append(s.pending, core.WarningEvent{Warning: w})
	}
	return s
}

func (s *stream) Next() (core.Event, error) {
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		return event, nil
	}
	if s.done {
		return nil, io.EOF
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" || strings.HasPrefix(line, "event: ") || line[0] == ':' {
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			if trimmed, found := strings.CutPrefix(line, "data:"); found {
				data = strings.TrimSpace(trimmed)
				ok = true
			}
		}
		if !ok || data == "" {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, core.NewProviderErrorWithCause("anthropic", core.ErrorTypeProvider, "anthropic: parse stream chunk", err)
		}
		events, err := s.events(chunk, json.RawMessage(data))
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			continue
		}
		s.pending = append(s.pending, events[1:]...)
		return events[0], nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, core.NewNetworkError("anthropic", "stream read error", err)
	}
	s.done = true
	// message_stop is a transport terminator. A message_delta carrying a
	// stop_reason is already the semantic completion signal, and some
	// compatible gateways close immediately after it without sending
	// message_stop. Preserve the accumulated response in that case.
	if s.finish != "" {
		return core.DoneEvent{FinishReason: s.finish, Provider: "anthropic", Model: s.model}, nil
	}
	return nil, core.NewNetworkError("anthropic", "anthropic: stream ended before message_stop without stop_reason", io.ErrUnexpectedEOF)
}

func (s *stream) Close() error {
	return s.resp.Body.Close()
}

func (s *stream) events(chunk streamChunk, raw json.RawMessage) ([]core.Event, error) {
	switch chunk.Type {
	case "message_start":
		if chunk.Message != nil {
			if chunk.Message.Model != "" {
				s.model = chunk.Message.Model
			}
			if chunk.Message.Usage != nil {
				s.usage = convertStreamUsage(chunk.Message.Usage, s.model)
				return []core.Event{core.UsageEvent{Usage: s.usage}}, nil
			}
		}
	case "message_delta":
		if chunk.Usage != nil {
			s.mergeUsage(chunk.Usage)
		}
		if chunk.Delta != nil && chunk.Delta.StopReason != "" {
			s.finish = core.NormalizeFinishReason(chunk.Delta.StopReason)
		}
		if s.usage.HasTokens() {
			return []core.Event{core.UsageEvent{Usage: s.usage}}, nil
		}
	case "message_stop":
		s.done = true
		return []core.Event{core.DoneEvent{FinishReason: s.finish, Provider: "anthropic", Model: s.model}}, nil
	case "content_block_start":
		if chunk.ContentBlock == nil {
			return nil, nil
		}
		switch chunk.ContentBlock.Type {
		case "tool_use":
			s.toolIDs[chunk.Index] = chunk.ContentBlock.ID
			s.toolNames[chunk.Index] = chunk.ContentBlock.Name
			return []core.Event{core.ToolUseStart{
				ID:        chunk.ContentBlock.ID,
				Name:      chunk.ContentBlock.Name,
				Index:     core.IntPtr(chunk.Index),
				Signature: chunk.ContentBlock.Signature,
			}}, nil
		case "thinking":
			if !s.includeReasoning {
				return nil, nil
			}
			if chunk.ContentBlock.Thinking == "" && chunk.ContentBlock.Signature == "" {
				return nil, nil
			}
			return []core.Event{core.ReasoningDelta{
				Text:      chunk.ContentBlock.Thinking,
				Signature: chunk.ContentBlock.Signature,
				Index:     core.IntPtr(chunk.Index),
			}}, nil
		case "redacted_thinking":
			if !s.includeReasoning {
				return nil, nil
			}
			if chunk.ContentBlock.Data == "" {
				return nil, nil
			}
			return []core.Event{core.ReasoningDelta{
				Redacted: []byte(chunk.ContentBlock.Data),
				Index:    core.IntPtr(chunk.Index),
			}}, nil
		case "text":
			return nil, nil
		default:
			return []core.Event{core.ProviderEvent{Name: chunk.Type + "." + chunk.ContentBlock.Type, Raw: raw}}, nil
		}
	case "content_block_delta":
		if chunk.Delta == nil {
			return nil, nil
		}
		switch chunk.Delta.Type {
		case "text_delta":
			return []core.Event{core.ContentDelta{Text: chunk.Delta.Text, ContentIndex: core.IntPtr(chunk.Index)}}, nil
		case "thinking_delta":
			if !s.includeReasoning {
				return nil, nil
			}
			return []core.Event{core.ReasoningDelta{Text: chunk.Delta.Thinking, Index: core.IntPtr(chunk.Index)}}, nil
		case "signature_delta":
			if !s.includeReasoning {
				return nil, nil
			}
			return []core.Event{core.ReasoningDelta{Signature: chunk.Delta.Signature, Index: core.IntPtr(chunk.Index)}}, nil
		case "input_json_delta":
			return []core.Event{core.ToolUseDelta{
				ID:             s.toolIDs[chunk.Index],
				Index:          core.IntPtr(chunk.Index),
				ArgumentsDelta: []byte(chunk.Delta.PartialJSON),
			}}, nil
		default:
			return []core.Event{core.ProviderEvent{Name: chunk.Type + "." + chunk.Delta.Type, Raw: raw}}, nil
		}
	case "content_block_stop":
		if id := s.toolIDs[chunk.Index]; id != "" {
			return []core.Event{core.ToolUseDone{ID: id, Index: core.IntPtr(chunk.Index)}}, nil
		}
	case "ping":
		return nil, nil
	case "error":
		if chunk.Error != nil {
			return nil, core.NewProviderError("anthropic", core.ErrorTypeProvider, fmt.Sprintf("anthropic: stream error: [%s] %s", chunk.Error.Type, chunk.Error.Message))
		}
		return nil, core.NewProviderError("anthropic", core.ErrorTypeProvider, "anthropic: unknown stream error")
	default:
		return []core.Event{core.ProviderEvent{Name: chunk.Type, Raw: raw}}, nil
	}
	return nil, nil
}

func convertStreamUsage(u *anthropicUsage, model string) core.Usage {
	if u == nil {
		return core.Usage{}
	}
	return core.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		TotalTokens:      u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens + u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		Provider:         "anthropic",
		Model:            model,
	}
}

func (s *stream) mergeUsage(u *anthropicUsage) {
	next := convertStreamUsage(u, s.model)
	if next.InputTokens > 0 || u.CacheReadInputTokens > 0 {
		s.usage.InputTokens = next.InputTokens
		s.usage.CacheReadTokens = next.CacheReadTokens
	}
	if next.OutputTokens > 0 {
		s.usage.OutputTokens = next.OutputTokens
	}
	if next.CacheWriteTokens > 0 {
		s.usage.CacheWriteTokens = next.CacheWriteTokens
	}
	s.usage.TotalTokens = s.usage.InputTokens + s.usage.CacheReadTokens + s.usage.CacheWriteTokens + s.usage.OutputTokens
	s.usage.Provider = "anthropic"
	s.usage.Model = s.model
}
