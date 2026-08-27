package openai

import (
	"bufio"
	"encoding/json"
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
	toolIDs          map[int]string
	finish           core.FinishReason
}

func newStream(resp *http.Response, req *core.Request) *stream {
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &stream{
		resp:             resp,
		scanner:          scanner,
		includeReasoning: thinkingEnabled(req),
		model:            req.Model,
		toolIDs:          make(map[int]string),
	}
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
		if line == "" || line[0] == ':' {
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			if trimmed, found := strings.CutPrefix(line, "data:"); found {
				data = strings.TrimSpace(trimmed)
				ok = true
			}
		}
		if !ok {
			continue
		}
		if data == "[DONE]" {
			s.done = true
			return core.DoneEvent{FinishReason: s.finish, Provider: "openai", Model: s.model}, nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, core.NewProviderErrorWithCause("openai", core.ErrorTypeProvider, "openai: parse stream chunk", err)
		}
		if chunk.Model != "" {
			s.model = chunk.Model
		}
		events := s.events(chunk)
		if len(events) == 0 {
			continue
		}
		s.pending = append(s.pending, events[1:]...)
		return events[0], nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, core.NewNetworkError("openai", "stream read error", err)
	}
	s.done = true
	return nil, core.NewProviderError("openai", core.ErrorTypeProvider, "openai: stream ended before [DONE]")
}

func (s *stream) Close() error {
	return s.resp.Body.Close()
}

func (s *stream) events(chunk streamChunk) []core.Event {
	events := make([]core.Event, 0, 4)
	if chunk.Usage != nil {
		events = append(events, core.UsageEvent{Usage: convertUsage(chunk.Usage, s.model)})
	}
	for _, choice := range chunk.Choices {
		// Reasoning must be emitted before text: a combined delta can
		// carry both at the reasoning→answer transition. Reasoning always
		// precedes the answer, so emitting text first would fragment the
		// EventCollector's blocks. Mirrors zendev-sh/goai #119.
		if s.includeReasoning {
			if text, summary := extractDeltaReasoning(choice.Delta); text != "" {
				events = append(events, core.ReasoningDelta{
					Text:    text,
					Summary: summary,
					Index:   core.IntPtr(choice.Index),
				})
			}
		}
		if choice.Delta.Content != "" {
			events = append(events, core.ContentDelta{
				Text:        choice.Delta.Content,
				OutputIndex: core.IntPtr(choice.Index),
			})
		}
		if choice.Delta.Refusal != "" {
			events = append(events, core.RefusalDelta{
				Text:        choice.Delta.Refusal,
				OutputIndex: core.IntPtr(choice.Index),
			})
		}
		for _, call := range choice.Delta.ToolCalls {
			index := call.Index
			id := call.ID
			if id != "" {
				s.toolIDs[index] = id
			} else {
				id = s.toolIDs[index]
			}
			if call.ID != "" || (call.Function != nil && call.Function.Name != "") {
				start := core.ToolUseStart{
					ID:    id,
					Name:  "",
					Index: &index,
				}
				if call.Function != nil {
					start.Name = call.Function.Name
				}
				events = append(events, start)
			}
			if call.Function != nil && call.Function.Arguments != "" {
				events = append(events, core.ToolUseDelta{
					ID:             id,
					Index:          &index,
					ArgumentsDelta: []byte(call.Function.Arguments),
				})
			}
		}
		if choice.FinishReason != "" {
			s.finish = core.NormalizeFinishReason(choice.FinishReason)
		}
	}
	return events
}
