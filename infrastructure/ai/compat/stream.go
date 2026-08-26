package compat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"nusashell/infrastructure/ai/core"
)

type stream struct {
	resp          *http.Response
	scanner       *bufio.Scanner
	req           *core.Request
	spec          Spec
	pending       []core.Event
	done          bool
	model         string
	usage         core.Usage
	finish        core.FinishReason
	lastContent   string
	lastReasoning string
	toolIDs       map[toolKey]string
	toolStarted   map[toolKey]bool
	toolPending   map[toolKey]*pendingTool
}

// pendingTool buffers a tool call whose opening chunk did not carry a name.
// Several OpenAI-compatible gateways (vLLM/sglang deployments, relay services)
// send the first delta with only an id and deliver function.name in a later
// chunk. Emitting ToolUseStart with an empty name at that point loses the name
// forever — ToolUseDelta has no channel to backfill it — and the consumer ends
// up dispatching `tool "" not found`. Buffer until the name arrives (or
// finish_reason forces a close), then flush Start + accumulated arguments.
type pendingTool struct {
	name string
	args strings.Builder
}

func newStream(resp *http.Response, req *core.Request, spec Spec) *stream {
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	return &stream{
		resp:        resp,
		scanner:     scanner,
		req:         req,
		spec:        spec,
		model:       req.Model,
		toolIDs:     make(map[toolKey]string),
		toolStarted: make(map[toolKey]bool),
		toolPending: make(map[toolKey]*pendingTool),
	}
}

type toolKey struct {
	choice int
	call   int
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
		data, ok := strings.CutPrefix(line, s.spec.dataPrefix())
		if !ok {
			if trimmed, found := strings.CutPrefix(line, "data:"); found {
				data = strings.TrimSpace(trimmed)
				ok = true
			}
		}
		if !ok {
			continue
		}
		if data == s.spec.doneSentinel() {
			s.done = true
			return core.DoneEvent{FinishReason: s.finish, Provider: s.spec.providerName(), Model: s.model}, nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, core.NewProviderErrorWithCause(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: parse stream chunk", s.spec.providerName()), err)
		}
		events, err := s.events(chunk)
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
		return nil, core.NewNetworkError(s.spec.providerName(), "stream read error", err)
	}
	// Clean EOF without the [DONE] sentinel. Two cases:
	//
	// 1. The provider sent a finish_reason in the last chunk but omitted
	//    the sentinel (OpenCode/Zen, TokenRouter, local gateways). This
	//    is a normal end of stream — synthesize a DoneEvent from the
	//    accumulated finish reason so core.Handle completes normally.
	// 2. The connection was cut mid-stream (no finish_reason, no
	//    sentinel). This is a transient failure — surface it so the
	//    retry loop can reconnect and continue.
	//
	// Mid-stream cuts with a scanner error are handled above; idle
	// stalls are handled by the watchdog.
	s.done = true
	if s.finish != "" {
		return core.DoneEvent{FinishReason: s.finish, Provider: s.spec.providerName(), Model: s.model}, nil
	}
	return nil, core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: stream ended before %s without finish_reason", s.spec.providerName(), s.spec.doneSentinel()))
}

func (s *stream) Close() error {
	return s.resp.Body.Close()
}

func (s *stream) events(chunk streamChunk) ([]core.Event, error) {
	events := make([]core.Event, 0, 4)
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if len(chunk.Usage) > 0 {
		var usage usage
		if err := json.Unmarshal(chunk.Usage, &usage); err != nil {
			return nil, core.NewProviderErrorWithCause(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: parse usage", s.spec.providerName()), err)
		}
		s.usage = convertUsage(usage, s.spec, s.spec.providerName(), s.model)
		events = append(events, core.UsageEvent{Usage: s.usage})
	}
	for _, choice := range chunk.Choices {
		if len(choice.Delta) > 0 {
			var delta map[string]any
			if err := json.Unmarshal(choice.Delta, &delta); err != nil {
				return nil, core.NewProviderErrorWithCause(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: parse delta", s.spec.providerName()), err)
			}
			if text := s.findContent(delta); text != "" {
				if s.contentCumulativeAllowed() {
					next, err := s.contentDelta(text)
					if err != nil {
						return nil, err
					}
					text = next
				}
				if text != "" {
					events = append(events, core.ContentDelta{Text: text, OutputIndex: core.IntPtr(choice.Index)})
				}
			}
			if refusal, _ := delta["refusal"].(string); refusal != "" {
				events = append(events, core.RefusalDelta{Text: refusal, OutputIndex: core.IntPtr(choice.Index)})
			}
			if s.reasoningAllowed() {
				reasoning := findReasoning(delta, s.reasoningFields())
				extra, err := reasoningExtra(delta)
				if err != nil {
					return nil, core.NewProviderErrorWithCause(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: convert reasoning details", s.spec.providerName()), err)
				}
				if reasoning != "" || len(extra) > 0 {
					extraFull := s.spec.Stream.ReasoningCumulative
					if s.spec.Stream.ReasoningCumulative && reasoning != "" {
						next, err := s.reasoningDelta(reasoning)
						if err != nil {
							return nil, err
						}
						reasoning = next
					}
					if reasoning != "" || len(extra) > 0 {
						events = append(events, core.ReasoningDelta{Text: reasoning, Extra: extra, ExtraFull: extraFull, Index: core.IntPtr(choice.Index)})
					}
				}
			}
			if rawCalls, ok := delta["tool_calls"].([]any); ok {
				for _, raw := range rawCalls {
					toolEvents, err := s.toolEvents(raw, choice.Index)
					if err != nil {
						return nil, err
					}
					events = append(events, toolEvents...)
				}
			}
		}
		if choice.FinishReason != "" {
			s.finish = core.NormalizeFinishReason(choice.FinishReason)
			// Compat providers signal tool-call completion via finish_reason
			// rather than a per-call terminator. Emit ToolUseDone for every open
			// call so consumers can finalize arguments — matching the native
			// anthropic/openai/gemini streams.
			events = append(events, s.toolDoneEvents(choice.Index)...)
		}
	}
	return events, nil
}

// toolDoneEvents closes tool calls opened for a choice, in tool index order.
// Compat providers carry no per-call terminator, so completion is inferred from
// finish_reason. Returns the events and clears the open set so a stream with
// multiple finish_reason chunks does not double-close.
//
// Calls still pending (the name never arrived) are flushed here with whatever
// the provider sent — an empty name surfaces as a visible downstream error
// instead of the call being silently swallowed.
func (s *stream) toolDoneEvents(choiceIndex int) []core.Event {
	if len(s.toolStarted) == 0 && len(s.toolPending) == 0 {
		return nil
	}
	keys := make([]toolKey, 0, len(s.toolStarted)+len(s.toolPending))
	for key := range s.toolStarted {
		if key.choice == choiceIndex {
			keys = append(keys, key)
		}
	}
	for key := range s.toolPending {
		if key.choice == choiceIndex {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].choice != keys[j].choice {
			return keys[i].choice < keys[j].choice
		}
		return keys[i].call < keys[j].call
	})
	events := make([]core.Event, 0, len(keys))
	for _, key := range keys {
		if p := s.toolPending[key]; p != nil {
			events = append(events, s.flushPending(key, p)...)
		}
		events = append(events, core.ToolUseDone{
			ID:          s.toolIDs[key],
			Index:       core.IntPtr(key.call),
			OutputIndex: core.IntPtr(key.choice),
		})
		delete(s.toolStarted, key)
	}
	return events
}

func (s *stream) findContent(delta map[string]any) string {
	fields := s.spec.Stream.ContentFields
	if len(fields) == 0 {
		fields = []string{"content"}
	}
	for _, field := range fields {
		if text, _ := delta[field].(string); text != "" {
			return text
		}
	}
	return ""
}

func (s *stream) contentDelta(current string) (string, error) {
	if s.lastContent == "" {
		s.lastContent = current
		return current, nil
	}
	if !strings.HasPrefix(current, s.lastContent) {
		return "", core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: cumulative content stream changed unexpectedly", s.spec.providerName()))
	}
	next := strings.TrimPrefix(current, s.lastContent)
	s.lastContent = current
	return next, nil
}

func (s *stream) contentCumulativeAllowed() bool {
	if !s.spec.Stream.ContentCumulative {
		return false
	}
	cond := s.spec.Stream.ContentCumulativeCondition
	if cond == "" || cond == "always" {
		return true
	}
	if cond == "thinking_enabled" {
		if s.req == nil || s.req.Thinking == nil || s.req.Thinking.Mode == core.ThinkingUnspecified {
			return true
		}
		return s.req.Thinking.Mode == core.ThinkingEnabled
	}
	return true
}

func (s *stream) reasoningAllowed() bool {
	if s.req != nil && s.req.Thinking != nil && s.req.Thinking.Mode == core.ThinkingDisabled {
		return false
	}
	cond := s.spec.Stream.ReasoningCondition
	if cond == "" || cond == "always" {
		return true
	}
	if after, ok := strings.CutPrefix(cond, "model_contains:"); ok {
		return strings.Contains(strings.ToLower(s.model), strings.ToLower(after))
	}
	return true
}

func (s *stream) reasoningFields() []string {
	if len(s.spec.Stream.ReasoningFields) > 0 {
		return s.spec.Stream.ReasoningFields
	}
	if len(s.spec.Response.ReasoningFields) > 0 {
		return s.spec.Response.ReasoningFields
	}
	return []string{"reasoning_summary", "reasoning_details", "reasoning_content", "reasoning", "reasoning_text"}
}

func reasoningExtra(delta map[string]any) (json.RawMessage, error) {
	details, ok := delta["reasoning_details"]
	if !ok || details == nil {
		return nil, nil
	}
	return json.Marshal(details)
}

func (s *stream) reasoningDelta(current string) (string, error) {
	if s.lastReasoning == "" {
		s.lastReasoning = current
		return current, nil
	}
	if !strings.HasPrefix(current, s.lastReasoning) {
		return "", core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: cumulative reasoning stream changed unexpectedly", s.spec.providerName()))
	}
	next := strings.TrimPrefix(current, s.lastReasoning)
	s.lastReasoning = current
	return next, nil
}

func (s *stream) toolEvents(raw any, choiceIndex int) ([]core.Event, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: stream tool_call must be an object", s.spec.providerName()))
	}
	index := choiceIndex
	if v, ok := m["index"]; ok {
		number, ok := v.(float64)
		if !ok || number != float64(int(number)) {
			return nil, core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: stream tool_call index must be integer", s.spec.providerName()))
		}
		index = int(number)
	}
	id, err := optionalString(m, "id", s.spec.providerName(), "stream tool_call")
	if err != nil {
		return nil, err
	}
	key := toolKey{choice: choiceIndex, call: index}
	if id != "" {
		s.toolIDs[key] = id
	} else {
		id = s.toolIDs[key]
	}
	var name, args string
	if rawFn, ok := m["function"]; ok {
		fn, ok := rawFn.(map[string]any)
		if !ok {
			return nil, core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: stream tool_call function must be an object", s.spec.providerName()))
		}
		name, err = optionalString(fn, "name", s.spec.providerName(), "stream tool_call function")
		if err != nil {
			return nil, err
		}
		args, err = optionalString(fn, "arguments", s.spec.providerName(), "stream tool_call function")
		if err != nil {
			return nil, err
		}
	} else if _, hasType := m["type"]; hasType {
		return nil, core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: stream tool_call missing function object", s.spec.providerName()))
	}
	events := make([]core.Event, 0, 2)
	// Emit ToolUseStart only the first time we see a tool-call index. OpenAI's
	// streaming protocol carries id/name only on the opening chunk; subsequent
	// chunks deliver argument deltas (often with the id omitted, which we backfill
	// above). Without this guard a backfilled id would re-trigger a start for every
	// delta, splitting one call into several empty-named duplicates.
	if s.toolStarted[key] {
		if args != "" {
			events = append(events, core.ToolUseDelta{ID: id, Index: core.IntPtr(index), OutputIndex: core.IntPtr(choiceIndex), ArgumentsDelta: []byte(args)})
		}
		return events, nil
	}
	if id == "" && name == "" && args == "" {
		return events, nil
	}
	// Not started yet: hold the call until we know its name. Keep the first
	// non-empty name and ignore later ones — gateways that resend the full name
	// on every chunk would corrupt a concatenating accumulator, and genuinely
	// fragmented names are unheard of (names are short single tokens).
	p := s.toolPending[key]
	if p == nil {
		p = &pendingTool{}
		s.toolPending[key] = p
	}
	if p.name == "" {
		p.name = name
	}
	p.args.WriteString(args)
	if p.name == "" {
		return events, nil
	}
	events = append(events, s.flushPending(key, p)...)
	return events, nil
}

// flushPending promotes a buffered call to started: emits ToolUseStart with the
// resolved name plus one ToolUseDelta carrying the arguments accumulated while
// the name was outstanding.
func (s *stream) flushPending(key toolKey, p *pendingTool) []core.Event {
	s.toolStarted[key] = true
	delete(s.toolPending, key)
	id := s.toolIDs[key]
	events := []core.Event{
		core.ToolUseStart{ID: id, Name: p.name, Index: core.IntPtr(key.call), OutputIndex: core.IntPtr(key.choice)},
	}
	if p.args.Len() > 0 {
		events = append(events, core.ToolUseDelta{ID: id, Index: core.IntPtr(key.call), OutputIndex: core.IntPtr(key.choice), ArgumentsDelta: []byte(p.args.String())})
	}
	return events
}

func optionalString(m map[string]any, key, provider, context string) (string, error) {
	value, ok := m[key]
	if !ok || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", core.NewProviderError(provider, core.ErrorTypeProvider, fmt.Sprintf("%s: %s %s must be string", provider, context, key))
	}
	return text, nil
}
