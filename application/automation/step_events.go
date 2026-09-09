package automation

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"nusashell/domain"
	"nusashell/pkg/text"
)

// Step lifecycle kinds / phases mirror application/agent observer events.
const (
	StepKindRun       = "run"
	StepKindStep      = "step"
	StepKindToolCall  = "tool_call"
	StepKindReasoning = "reasoning"
	StepKindText      = "text"

	StepPhasePre  = "pre"
	StepPhasePost = "post"

	StepStatusRunning = "running"
	StepStatusOK      = "ok"
	StepStatusError   = "error"
)

const (
	notifyDetailCap        = 200
	notifyMsgMapCap        = 64 // bound callID→message_id entries per agent run
	notifyProgressTool     = "internal_send_progress"
	notifyProgressFallback = "admin.send_progress"
)

// StepLifecycleEvent is the automation-facing shape of an agent step
// lifecycle signal, enriched with workflow/run identity for sinks.
type StepLifecycleEvent struct {
	Kind           string
	Phase          string
	RunID          string
	WorkflowID     string
	JobID          string
	StepID         string
	ConversationID string
	AgentRunID     string
	Round          int
	CallID         string
	ToolName       string
	Status         string
	Detail         string
	Error          string
	Notify         *domain.NotifyConfig
	TriggerEvent   *domain.Event
}

// StepEventSink receives agent-step lifecycle events. Implementations must
// be safe to call from a detached goroutine and must not block the run.
type StepEventSink interface {
	OnStepEvent(ctx context.Context, ev StepLifecycleEvent) error
}

type activeAgentStep struct {
	Run    *domain.WorkflowRun
	JobID  string
	StepID string
}

// AddStepEventSink registers a best-effort lifecycle sink.
func (s *ExecutionScheduler) AddStepEventSink(sink StepEventSink) {
	if s == nil || sink == nil {
		return
	}
	s.mu.Lock()
	s.stepSinks = append(s.stepSinks, sink)
	s.mu.Unlock()
}

func (s *ExecutionScheduler) bindActiveAgentStep(convID string, run *domain.WorkflowRun, jobID, stepID string) {
	if s == nil || convID == "" || run == nil {
		return
	}
	if s.activeByConv == nil {
		s.activeByConv = &sync.Map{}
	}
	s.activeByConv.Store(convID, &activeAgentStep{Run: run, JobID: jobID, StepID: stepID})
}

func (s *ExecutionScheduler) unbindActiveAgentStep(convID string) {
	if s == nil || s.activeByConv == nil || convID == "" {
		return
	}
	s.activeByConv.Delete(convID)
}

// ForwardAgentLifecycle routes a headless agent lifecycle event to registered
// sinks asynchronously. Unknown conversations (no active step) are ignored.
func (s *ExecutionScheduler) ForwardAgentLifecycle(ev StepLifecycleEvent) {
	if s == nil || ev.ConversationID == "" {
		return
	}
	var active *activeAgentStep
	if s.activeByConv != nil {
		if v, ok := s.activeByConv.Load(ev.ConversationID); ok {
			active = v.(*activeAgentStep)
		}
	}
	if active == nil || active.Run == nil {
		return
	}
	ev.RunID = active.Run.ID
	ev.WorkflowID = active.Run.WorkflowID
	ev.JobID = active.JobID
	ev.StepID = active.StepID
	ev.Notify = active.Run.Definition.Notify
	ev.TriggerEvent = active.Run.Event

	s.mu.Lock()
	sinks := append([]StepEventSink(nil), s.stepSinks...)
	s.mu.Unlock()
	for _, sink := range sinks {
		sink := sink
		event := ev
		s.goSafe("automation-step-sink", func() {
			defer func() { _ = recover() }()
			_ = sink.OnStepEvent(context.Background(), event)
		})
	}
}

// NotifyProgressSink forwards step lifecycle events to an MCP plugin tool
// (internal_send_progress, falling back to admin.send_progress). Delivery is
// best-effort: tool_call pre opens a progress message (stores message_id),
// tool_call post edits that placeholder; observer errors emit
// automation.notify.failed / skipped and never block or fail the run.
type NotifyProgressSink struct {
	Caller MCPToolCaller
	Bus    Emitter

	mu    sync.Mutex
	byRun map[string]*notifyRunState // key: AgentRunID
}

type notifyRunState struct {
	messages       map[string]string // callID → telegram message_id
	reasoningRound int               // last round that sent reasoning
	lastMessageID  string            // most recent progress message (step final edit)
}

func NewNotifyProgressSink(caller MCPToolCaller, bus Emitter) *NotifyProgressSink {
	return &NotifyProgressSink{
		Caller: caller,
		Bus:    bus,
		byRun:  map[string]*notifyRunState{},
	}
}

func (n *NotifyProgressSink) OnStepEvent(ctx context.Context, ev StepLifecycleEvent) error {
	if n == nil || !ev.Notify.Enabled() {
		return nil
	}
	// Run events are for other observers; notify tracks step/tool/content.
	if ev.Kind == StepKindRun {
		if ev.Phase == StepPhasePost {
			n.clearRun(ev.AgentRunID)
		}
		return nil
	}
	detail := ev.Notify.NormalizedDetail()
	switch ev.Kind {
	case StepKindStep:
		if ev.Phase == StepPhasePre {
			// No placeholder for the step itself — tool progress and the
			// final reply are the only chat-visible items.
			return nil
		}
		if ev.Phase == StepPhasePost {
			// Deliver exactly ONE final message: the agent's final output
			// (or a bounded error note). Intermediate per-round text events
			// are never forwarded — they leak working-note narration.
			detail := ev.Detail
			eventType := "step_ended"
			if status := ev.Status; status == StepStatusError || ev.Error != "" {
				detail = ev.Error
				if detail == "" {
					detail = ev.Status
				}
			}
			if detail != "" && ev.Notify.NormalizedDetail() != domain.NotifyDetailNone {
				if _, err := n.deliverOpen(ctx, ev, eventType, ev.Status, extractReplyText(detail)); err != nil {
					return err
				}
			}
			n.clearRun(ev.AgentRunID)
			return nil
		}
	case StepKindToolCall:
		if !notifyWants(detail, ev.Kind) {
			return nil
		}
		if ev.Phase == StepPhasePre {
			return n.openToolProgress(ctx, ev)
		}
		if ev.Phase == StepPhasePost {
			return n.editToolProgress(ctx, ev)
		}
	case StepKindReasoning:
		if !notifyWants(detail, ev.Kind) {
			return nil
		}
		return n.sendContentProgress(ctx, ev)
	case StepKindText:
		// Intermediate per-round text is dropped: the final reply is
		// delivered once from the step-end event.
		return nil
	}
	return nil
}

func notifyWants(detail domain.NotifyDetail, kind string) bool {
	switch detail {
	case domain.NotifyDetailNone:
		return false
	case domain.NotifyDetailTools:
		// Tool progress only — reasoning and assistant text stay hidden.
		return kind == StepKindToolCall
	case domain.NotifyDetailText:
		// Tool progress plus assistant text; reasoning stays hidden.
		return kind == StepKindToolCall || kind == StepKindText
	case domain.NotifyDetailAll:
		return true
	default:
		return kind == StepKindToolCall
	}
}

func (n *NotifyProgressSink) openToolProgress(ctx context.Context, ev StepLifecycleEvent) error {
	msgID, err := n.deliverOpen(ctx, ev, "tool_start", StepStatusRunning, truncateNotify(ev.Detail))
	if err != nil || msgID == "" || ev.CallID == "" {
		return err
	}
	n.storeMessageID(ev.AgentRunID, ev.CallID, msgID)
	return nil
}

func (n *NotifyProgressSink) editToolProgress(ctx context.Context, ev StepLifecycleEvent) error {
	status := ev.Status
	if status == "" {
		status = StepStatusOK
	}
	msgID := n.lookupMessageID(ev.AgentRunID, ev.CallID)
	return n.deliverEdit(ctx, ev, msgID, "tool_end", status, truncateNotify(ev.Detail))
}

func (n *NotifyProgressSink) sendContentProgress(ctx context.Context, ev StepLifecycleEvent) error {
	if ev.Kind == StepKindReasoning {
		n.mu.Lock()
		st := n.runState(ev.AgentRunID)
		if st.reasoningRound == ev.Round && ev.Round > 0 {
			n.mu.Unlock()
			return nil // one reasoning message per round
		}
		st.reasoningRound = ev.Round
		n.mu.Unlock()
	}
	status := StepStatusOK
	_, err := n.deliverOpen(ctx, ev, ev.Kind, status, truncateNotify(ev.Detail))
	return err
}

func (n *NotifyProgressSink) deliverOpen(ctx context.Context, ev StepLifecycleEvent, eventType, status, detail string) (string, error) {
	raw, err := n.callProgress(ctx, ev, eventType, status, detail, "")
	if err != nil {
		return "", err
	}
	msgID := parseProgressMessageID(raw)
	if msgID != "" {
		n.setLastMessageID(ev.AgentRunID, msgID)
	}
	return msgID, nil
}

func (n *NotifyProgressSink) deliverEdit(ctx context.Context, ev StepLifecycleEvent, messageID, eventType, status, detail string) error {
	if messageID == "" {
		_, err := n.deliverOpen(ctx, ev, eventType, status, detail)
		return err
	}
	_, err := n.callProgress(ctx, ev, eventType, status, detail, messageID)
	if err != nil {
		// Fallback: open a new progress message when edit fails.
		_, err2 := n.deliverOpen(ctx, ev, eventType, status, detail)
		return err2
	}
	return nil
}

func (n *NotifyProgressSink) callProgress(ctx context.Context, ev StepLifecycleEvent, eventType, status, detail, messageID string) (string, error) {
	chatID := ""
	if ev.Notify != nil && strings.TrimSpace(ev.Notify.ChatID) != "" {
		chatID = strings.TrimSpace(domain.RenderAgentPrompt(ev.Notify.ChatID, ev.TriggerEvent))
	}
	if chatID == "" {
		n.emitSkipped(ev, "missing_chat_id")
		return "", nil
	}
	if n.Caller == nil {
		n.emitSkipped(ev, "no_mcp_caller")
		return "", nil
	}
	plugin := strings.TrimSpace(ev.Notify.Plugin)
	payload := map[string]any{
		"chat_id":    chatID,
		"event_type": eventType,
		"status":     status,
		"title":      truncateNotify(ev.ToolName),
		"detail":     detail,
	}
	if messageID != "" {
		payload["message_id"] = messageID
	}
	serverID := "plugin:" + strings.TrimPrefix(plugin, "plugin:")
	raw, err := n.Caller.CallTool(ctx, serverID, notifyProgressTool, payload)
	if err != nil {
		raw2, err2 := n.Caller.CallTool(ctx, serverID, notifyProgressFallback, payload)
		if err2 != nil {
			n.emitFailed(ev, err.Error()+"; fallback: "+err2.Error())
			return "", err2
		}
		return raw2, nil
	}
	return raw, nil
}

func (n *NotifyProgressSink) runState(agentRunID string) *notifyRunState {
	if n.byRun == nil {
		n.byRun = map[string]*notifyRunState{}
	}
	key := agentRunID
	if key == "" {
		key = "_"
	}
	st := n.byRun[key]
	if st == nil {
		st = &notifyRunState{messages: map[string]string{}}
		n.byRun[key] = st
	}
	return st
}

func (n *NotifyProgressSink) storeMessageID(agentRunID, callID, msgID string) {
	if n == nil || callID == "" || msgID == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.runState(agentRunID)
	if len(st.messages) >= notifyMsgMapCap {
		// Drop an arbitrary entry to stay bounded.
		for k := range st.messages {
			delete(st.messages, k)
			break
		}
	}
	st.messages[callID] = msgID
	st.lastMessageID = msgID
}

func (n *NotifyProgressSink) lookupMessageID(agentRunID, callID string) string {
	if n == nil || callID == "" {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.byRun[agentRunID]
	if st == nil {
		return ""
	}
	return st.messages[callID]
}

func (n *NotifyProgressSink) lastMessageID(agentRunID string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.byRun[agentRunID]
	if st == nil {
		return ""
	}
	return st.lastMessageID
}

func (n *NotifyProgressSink) setLastMessageID(agentRunID, msgID string) {
	if n == nil || msgID == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.runState(agentRunID)
	st.lastMessageID = msgID
}

func (n *NotifyProgressSink) clearRun(agentRunID string) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.byRun, agentRunID)
	delete(n.byRun, "_")
}

func (n *NotifyProgressSink) emitSkipped(ev StepLifecycleEvent, reason string) {
	if n == nil || n.Bus == nil {
		return
	}
	n.Bus.Emit("automation.notify.skipped", map[string]any{
		"run_id": ev.RunID, "step_id": ev.StepID, "reason": reason,
	})
}

func (n *NotifyProgressSink) emitFailed(ev StepLifecycleEvent, errMsg string) {
	if n == nil || n.Bus == nil {
		return
	}
	n.Bus.Emit("automation.notify.failed", map[string]any{
		"run_id": ev.RunID, "step_id": ev.StepID, "error": truncateNotify(errMsg),
	})
}

func parseProgressMessageID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "ok" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) == nil {
		for _, key := range []string{"message_id", "messageId", "id"} {
			switch v := m[key].(type) {
			case string:
				if s := strings.TrimSpace(v); s != "" {
					return s
				}
			case float64:
				return strconv.FormatInt(int64(v), 10)
			case json.Number:
				return v.String()
			}
		}
	}
	if !strings.ContainsAny(raw, " \n\t{}[]") && len(raw) <= 64 {
		return raw
	}
	return ""
}

func truncateNotify(s string) string {
	return text.Truncate(strings.TrimSpace(s), notifyDetailCap)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// extractReplyText renders the agent's final output whatever the model
// produced (LLMs are unpredictable): object JSON {-reply-}, double-encoded
// JSON string, fenced json block, or plain text. It always returns a
// deliverable string for sendable final output — nothing is rejected on
// shape, and the empty string only means an empty model output.
func extractReplyText(detail string) string {
	s := strings.TrimSpace(detail)
	if s == "" {
		return ""
	}
	// Unwrap one level of JSON string (double-encoded output).
	if strings.HasPrefix(s, "\"") {
		var inner string
		if err := json.Unmarshal([]byte(s), &inner); err == nil {
			return extractReplyText(inner)
		}
	}
	// Strip a markdown/json fence if present.
	body := s
	if strings.HasPrefix(s, "```") {
		body = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(s, "```"), "```"))
		if idx := strings.IndexByte(body, '\n'); idx >= 0 && strings.Contains(body[:idx], "json") {
			body = body[idx+1:]
		}
	}
	// Structured reply object.
	var structured struct {
		Reply string `json:"reply"`
	}
	if json.Unmarshal([]byte(body), &structured) == nil && strings.TrimSpace(structured.Reply) != "" {
		return strings.TrimSpace(structured.Reply)
	}
	// Plain text: deliver as-is (fence already stripped).
	return body
}
