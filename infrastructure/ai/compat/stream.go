package compat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"nusashell/infrastructure/ai/core"
)

// defaultDrainWindow bounds how long the stream waits, after a finish_reason
// has been observed, for the trailing accounting chunk that OpenAI-compatible
// providers emit before [DONE]. The window only applies when the provider
// neither sends more data nor ends the response: the stream itself never
// requires [DONE] or the usage chunk to complete.
const defaultDrainWindow = time.Second

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

	// drainWindow is how long the stream waits after a finish_reason for the
	// trailing accounting chunk (OpenAI-compatible providers emit a final
	// usage-only chunk after the finish chunk) before completing on its own.
	drainWindow time.Duration
	// finished marks semantic completion: a valid finish_reason was observed.
	// From here the stream completes from the transport continuing (usage
	// chunk, sentinel, EOF, or any read error) instead of waiting for the
	// [DONE] trailer.
	finished     bool
	drainTimer   *time.Timer
	drainStopped atomic.Bool
}

// pendingTool tracks a tool call whose opening chunk did not carry a name.
// Several OpenAI-compatible gateways (vLLM/sglang deployments, relay
// services) send the first delta with only an id and deliver function.name in
// a later chunk. Do not emit ToolUseStart with an empty name: downstream
// dispatch would see `tool "" not found`. Argument deltas can still be emitted
// immediately for stream observers (the interactive UI uses them as an
// activity signal); the Start event waits until the name arrives.
type pendingTool struct {
	name string
	args strings.Builder // arguments received after the name is known
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
		drainWindow: defaultDrainWindow,
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
			// Transport trailer. It completes a stream that has not already
			// completed semantically, but is never required: with a
			// finish_reason observed the stream is already done, and the
			// sentinel must not produce a second DoneEvent.
			return s.complete(), nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			if s.finished {
				// Semantic completion already happened; trailing noise must
				// not fail or stall a turn the model already finished.
				continue
			}
			return nil, core.NewProviderErrorWithCause(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: parse stream chunk", s.spec.providerName()), err)
		}
		if err := streamPayloadError(s.spec.providerName(), chunk.Error); err != nil {
			return nil, err
		}
		events, err := s.events(chunk)
		if err != nil {
			return nil, err
		}
		if s.finished {
			if hasUsage(chunk.Usage) {
				// Final accounting. The usage chunk is the end of the
				// meaningful stream: the [DONE] that follows is only a
				// trailer, so there is nothing left to wait for.
				events = append(events, s.complete())
			} else {
				s.armDrain()
			}
		}
		if len(events) == 0 {
			continue
		}
		s.pending = append(s.pending, events[1:]...)
		return events[0], nil
	}
	if err := s.scanner.Err(); err != nil {
		if s.finished {
			// Read errors after finish_reason (connection reset, or the
			// drain window closing the body) are not partial turns: the
			// model already reported completion.
			return s.complete(), nil
		}
		return nil, core.NewNetworkError(s.spec.providerName(), "stream read error", err)
	}
	// Clean EOF without the [DONE] sentinel. Two cases:
	//
	// 1. The provider sent a finish_reason in the last chunk but omitted
	//    the sentinel (OpenCode/Zen, TokenRouter, local gateways). This
	//    is a normal end of stream.
	// 2. The connection was cut mid-stream (no finish_reason, no
	//    sentinel). This is a transient failure — surface it so the
	//    retry loop can reconnect and continue.
	//
	// Mid-stream cuts with a scanner error are handled above; idle
	// stalls are handled by the watchdog.
	if s.finished {
		return s.complete(), nil
	}
	return nil, core.NewNetworkError(s.spec.providerName(), fmt.Sprintf("%s: stream ended before %s without finish_reason", s.spec.providerName(), s.spec.doneSentinel()), io.ErrUnexpectedEOF)
}

// complete marks the stream finished and returns the single DoneEvent. Every
// terminal path routes through it, so a stream that already saw a
// finish_reason can never emit a second DoneEvent when a [DONE] sentinel or a
// repeated finish_reason (OpenRouter includes one on its usage chunk) arrives.
func (s *stream) complete() core.Event {
	s.stopDrain()
	s.done = true
	return core.DoneEvent{FinishReason: s.finish, Provider: s.spec.providerName(), Model: s.model}
}

// armDrain starts the post-finish drain window: after semantic completion the
// stream keeps reading (for the accounting chunk / the sentinel / EOF) but
// must not stall when the provider holds the connection open without sending
// anything else.
func (s *stream) armDrain() {
	if s.drainTimer != nil || s.drainStopped.Load() {
		return
	}
	s.drainTimer = time.AfterFunc(s.drainWindow, func() {
		if s.drainStopped.Load() {
			return
		}
		// Interrupt the blocked read: semantic completion already happened,
		// but the provider is holding the connection open without ending the
		// response. Closing the body unblocks the next read, which the
		// post-finish path turns into a normal DoneEvent.
		s.resp.Body.Close()
	})
}

func (s *stream) stopDrain() {
	if !s.drainStopped.CompareAndSwap(false, true) {
		return
	}
	if s.drainTimer != nil {
		s.drainTimer.Stop()
	}
}

// hasUsage reports whether a stream chunk carries an accounting payload. A
// JSON null (or an absent field) is not accounting.
func hasUsage(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return strings.TrimSpace(string(raw)) != "null"
}

// streamPayloadError converts an error reported inside an HTTP 200 SSE body
// into a provider error. Providers use both the object form
// {"error":{"message":...,"type":...}} and the string form {"error":"..."};
// an empty payload is not an error.
func streamPayloadError(provider string, raw json.RawMessage) error {
	text := strings.TrimSpace(string(raw))
	if len(raw) == 0 || text == "null" || text == `""` || text == "{}" {
		return nil
	}
	message := text
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && strings.TrimSpace(envelope.Message) != "" {
		message = strings.TrimSpace(envelope.Message)
	} else {
		var textValue string
		if err := json.Unmarshal(raw, &textValue); err == nil && strings.TrimSpace(textValue) != "" {
			message = strings.TrimSpace(textValue)
		}
	}
	return core.NewProviderError(provider, core.ErrorTypeProvider, fmt.Sprintf("%s: stream error: %s", provider, message))
}

func (s *stream) Close() error {
	s.stopDrain()
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
			// Reasoning must be emitted before text: a combined delta can
			// carry both at the reasoning→answer transition (seen with GLM
			// via NVIDIA NIM, DeepSeek via OpenRouter). Reasoning always
			// precedes the answer, so emitting text first would fragment
			// the EventCollector's blocks (Reasoning, Text, Reasoning,
			// Text instead of Reasoning, Text). Mirrors zendev-sh/goai #119.
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
			reason := core.NormalizeFinishReason(choice.FinishReason)
			if reason == core.FinishReasonError {
				// The one finish_reason that is not a completion: the
				// provider itself reported the turn failed. Surface it as an
				// error instead of an empty successful response.
				return nil, core.NewProviderError(s.spec.providerName(), core.ErrorTypeProvider, fmt.Sprintf("%s: stream reported finish_reason %q", s.spec.providerName(), choice.FinishReason))
			}
			s.finish = reason
			// Semantic completion: from here the stream completes from the
			// transport (usage chunk, [DONE], EOF, or an error) instead of
			// waiting for the sentinel.
			s.finished = true
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
	if p.name == "" && name == "" {
		// Preserve the stream's useful early signal without manufacturing an
		// invalid tool start. The EventCollector can safely aggregate a delta
		// before the later Start because both events identify the same call by
		// id/index. Do not retain these bytes in p.args: flushPending must not
		// replay them when the name eventually arrives.
		if args == "" {
			return events, nil
		}
		return []core.Event{core.ToolUseDelta{
			ID: id, Index: core.IntPtr(index), OutputIndex: core.IntPtr(choiceIndex), ArgumentsDelta: []byte(args),
		}}, nil
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
