package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"nusashell/infrastructure/ai/core"
)

// ResponsesStream decodes a Responses API SSE body into ResponseEvents. It
// mirrors codex-api/src/sse/responses.rs: response.completed is the terminal
// event, and a stream that closes before it is a stream error ("stream
// closed before response.completed"), not a silent success.
type ResponsesStream struct {
	reader       io.Reader
	closer       io.Closer
	scanner      *bufio.Scanner
	currentEvent string
	done         bool
}

// NewResponsesStream wraps an SSE body. If the body implements io.Closer,
// Close drains ownership of it.
func NewResponsesStream(body io.Reader) *ResponsesStream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	stream := &ResponsesStream{scanner: scanner}
	if closer, ok := body.(io.Closer); ok {
		stream.closer = closer
	}
	return stream
}

// Next returns the next decoded event. It returns io.EOF after
// response.completed. A close before response.completed returns a
// retryable network error.
func (s *ResponsesStream) Next() (ResponseEvent, error) {
	if s.done {
		return nil, io.EOF
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		name, ok := strings.CutPrefix(line, "event: ")
		if ok {
			s.currentEvent = name
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			trimmed, found := strings.CutPrefix(line, "data:")
			if !found {
				continue
			}
			data = strings.TrimSpace(trimmed)
		}
		event, terminal, err := s.decodeFrame(data)
		if err != nil {
			return nil, err
		}
		if event != nil {
			if terminal {
				s.done = true
			}
			return event, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, core.NewNetworkError("codex", "codex: responses stream read error", err)
	}
	s.done = true
	return nil, core.NewNetworkError("codex", "codex: stream closed before response.completed", io.ErrUnexpectedEOF)
}

// Close releases the underlying body when it owns a closer.
func (s *ResponsesStream) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

// decodeFrame decodes one SSE data frame. terminal reports whether the event
// ends the logical stream (response.completed); a nil event with nil error
// means the frame produced nothing to surface.
func (s *ResponsesStream) decodeFrame(data string) (ResponseEvent, bool, error) {
	raw := json.RawMessage(data)
	eventName := s.currentEvent
	s.currentEvent = ""
	if eventName == "" {
		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &peek); err == nil {
			eventName = peek.Type
		}
	}
	switch eventName {
	case "response.created":
		var created struct {
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
		}
		if err := json.Unmarshal(raw, &created); err != nil {
			return nil, false, parseError("codex: parse response.created", err)
		}
		return EventCreated{ResponseID: created.Response.ID}, false, nil
	case "response.output_item.added":
		item, err := decodeOutputItem(raw, eventName)
		if err != nil {
			return nil, false, err
		}
		return EventOutputItemAdded{Item: item}, false, nil
	case "response.output_item.done":
		item, err := decodeOutputItem(raw, eventName)
		if err != nil {
			return nil, false, err
		}
		return EventOutputItemDone{Item: item}, false, nil
	case "response.completed":
		var completed struct {
			Response struct {
				ID      string         `json:"id"`
				Usage   *responseUsage `json:"usage"`
				EndTurn *bool          `json:"end_turn"`
			} `json:"response"`
		}
		if err := json.Unmarshal(raw, &completed); err != nil {
			return nil, false, parseError("codex: parse response.completed", err)
		}
		event := EventCompleted{ResponseID: completed.Response.ID, EndTurn: completed.Response.EndTurn}
		if completed.Response.Usage != nil {
			usage := completed.Response.Usage.toTokenUsage()
			event.TokenUsage = &usage
			// Preserve the raw usage object for observability, mirroring the
			// upstream usage_metadata passthrough.
			if usageRaw := extractRawUsage(raw); len(usageRaw) > 0 {
				event.UsageMetadata = append(json.RawMessage(nil), usageRaw...)
			}
		}
		return event, true, nil
	case "response.failed":
		var failed struct {
			Response struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal(raw, &failed); err != nil {
			return nil, false, parseError("codex: parse response.failed", err)
		}
		return EventError{Err: core.NewProviderError("codex", core.ErrorTypeProvider,
			fmt.Sprintf("codex: response failed: [%s] %s", failed.Response.Error.Code, failed.Response.Error.Message))}, true, nil
	case "error":
		var responseErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &responseErr); err != nil {
			return nil, false, parseError("codex: parse responses stream error", err)
		}
		return EventError{Err: core.NewProviderError("codex", core.ErrorTypeProvider,
			"codex: stream error: "+responseErr.Error.Message)}, true, nil
	default:
		return EventOther{Type: eventName, Raw: append(json.RawMessage(nil), raw...)}, false, nil
	}
}

func decodeOutputItem(raw json.RawMessage, eventName string) (ResponseItem, error) {
	var frame struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		return ResponseItem{}, parseError("codex: parse "+eventName, err)
	}
	if len(frame.Item) == 0 {
		return ResponseItem{}, parseError("codex: "+eventName+" missing item", fmt.Errorf("empty item"))
	}
	var item ResponseItem
	if err := json.Unmarshal(frame.Item, &item); err != nil {
		return ResponseItem{}, parseError("codex: parse "+eventName+" item", err)
	}
	return item, nil
}

// responseUsage is the raw usage object on response.completed, mirroring
// codex-api/src/sse/responses.rs ResponseCompletedUsage.
type responseUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	InputTokensDetails *struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        int64 `json:"output_tokens"`
	OutputTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens int64 `json:"total_tokens"`
}

func (u responseUsage) toTokenUsage() TokenUsage {
	out := TokenUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.InputTokensDetails != nil {
		out.CachedInputTokens = u.InputTokensDetails.CachedTokens
		out.CacheWriteInputTokens = u.InputTokensDetails.CacheWriteTokens
	}
	if u.OutputTokensDetails != nil {
		out.ReasoningOutputTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return out
}

func parseError(message string, cause error) error {
	return core.NewProviderErrorWithCause("codex", core.ErrorTypeProvider, message, cause)
}

// extractRawUsage pulls the raw response.usage object out of a
// response.completed frame without a second full decode.
func extractRawUsage(frame json.RawMessage) json.RawMessage {
	var probe struct {
		Response struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		return nil
	}
	if len(probe.Response.Usage) == 0 || string(probe.Response.Usage) == "null" {
		return nil
	}
	return probe.Response.Usage
}
