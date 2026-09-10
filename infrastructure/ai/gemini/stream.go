package gemini

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"nusashell/infrastructure/ai/core"
)

// stream decodes the SSE stream from models/{model}:streamGenerateContent?alt=sse.
// Every frame is one generateContentResponse chunk; Gemini sends complete
// functionCall parts rather than incremental argument deltas.
type stream struct {
	resp    *http.Response
	scanner *bufio.Scanner
	pending []core.Event
	done    bool
	model   string
	usage   core.Usage

	finish       core.FinishReason
	finishRaw    string
	sawToolCalls bool
	toolIndex    int
}

func newStream(resp *http.Response, req *core.Request, warnings []core.Warning) *stream {
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	s := &stream{
		resp:    resp,
		scanner: scanner,
		model:   req.Model,
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
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		data, ok := dataField(line)
		if !ok || data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk generateContentResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, core.NewProviderErrorWithCause(providerName, core.ErrorTypeProvider, "gemini: parse stream chunk", err)
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
		return nil, core.NewNetworkError(providerName, "stream read error", err)
	}
	s.done = true
	if s.finish != "" || s.finishRaw != "" {
		return core.DoneEvent{FinishReason: s.finish, FinishReasonRaw: s.finishRaw, Provider: providerName, Model: s.model}, nil
	}
	return nil, core.NewNetworkError(providerName, "gemini: stream ended before a finish reason", io.ErrUnexpectedEOF)
}

func (s *stream) Close() error {
	return s.resp.Body.Close()
}

func dataField(line string) (string, bool) {
	if data, ok := strings.CutPrefix(line, "data: "); ok {
		return data, true
	}
	if data, ok := strings.CutPrefix(line, "data:"); ok {
		return strings.TrimSpace(data), true
	}
	return "", false
}

func (s *stream) events(chunk generateContentResponse, raw json.RawMessage) ([]core.Event, error) {
	// Gemini can report an error inside an HTTP 200 stream.
	if chunk.Error != nil {
		return nil, core.NewHTTPError(providerName, errorStatus(chunk.Error), string(raw))
	}
	if chunk.ModelVersion != "" {
		s.model = normalizeModelID(chunk.ModelVersion)
	}
	var events []core.Event
	if chunk.UsageMetadata != nil {
		s.usage = convertUsage(chunk.UsageMetadata)
		s.usage.Model = s.model
		events = append(events, core.UsageEvent{Usage: s.usage})
	}
	if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
		s.finish = core.FinishReasonSafety
		s.finishRaw = chunk.PromptFeedback.BlockReason
		events = append(events, core.WarningEvent{Warning: warning("gemini.prompt_blocked", "prompt blocked by Gemini ("+chunk.PromptFeedback.BlockReason+")")})
	}
	for _, candidate := range chunk.Candidates {
		if candidate.Content != nil {
			partEvents, err := s.partEvents(candidate.Content.Parts)
			if err != nil {
				return nil, err
			}
			events = append(events, partEvents...)
		}
		if candidate.FinishReason != "" {
			s.finishRaw = candidate.FinishReason
			s.finish = core.NormalizeFinishReason(candidate.FinishReason)
			// Gemini reports tool calls and the finish reason in separate
			// chunks; a turn that produced calls finishes on tool use.
			if s.sawToolCalls && s.finish == core.FinishReasonStop {
				s.finish = core.FinishReasonToolCall
			}
		}
	}
	if s.finish != "" || s.finishRaw != "" {
		events = append(events, core.DoneEvent{FinishReason: s.finish, FinishReasonRaw: s.finishRaw, Provider: providerName, Model: s.model})
		s.done = true
	}
	return events, nil
}

func (s *stream) partEvents(parts []part) ([]core.Event, error) {
	events := make([]core.Event, 0, len(parts))
	for i, p := range parts {
		index := core.IntPtr(i)
		switch {
		case p.FunctionCall != nil:
			call := p.FunctionCall
			id := strings.TrimSpace(call.ID)
			if id == "" {
				id = newToolCallID()
			}
			args := call.Args
			if len(args) == 0 || !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			toolIndex := core.IntPtr(s.toolIndex)
			// Gemini delivers whole calls, so the start/argument/done triple is
			// emitted together and the call is complete when it reaches the
			// collector.
			events = append(events,
				core.ToolUseStart{ID: id, Name: call.Name, Index: toolIndex, Signature: p.ThoughtSignature},
				core.ToolUseDelta{ID: id, Index: toolIndex, ArgumentsDelta: args},
				core.ToolUseDone{ID: id, Index: toolIndex},
			)
			s.toolIndex++
			s.sawToolCalls = true
		case p.FunctionResponse != nil:
			// History echo from context circulation; nothing to emit.
		case p.Thought:
			if p.Text == "" && p.ThoughtSignature == "" {
				continue
			}
			events = append(events, core.ReasoningDelta{
				Text:      p.Text,
				Signature: p.ThoughtSignature,
				Extra:     signatureExtra(p.ThoughtSignature),
				ExtraFull: true,
				Index:     index,
			})
		default:
			if p.ThoughtSignature != "" {
				events = append(events, core.ReasoningDelta{
					Signature: p.ThoughtSignature,
					Extra:     signatureExtra(p.ThoughtSignature),
					ExtraFull: true,
					Index:     index,
				})
			}
			if p.Text != "" {
				events = append(events, core.ContentDelta{Text: p.Text, ContentIndex: index})
			}
		}
	}
	return events, nil
}
