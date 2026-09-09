package automation

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"nusashell/domain"
	"nusashell/pkg/text"
)

// Step lifecycle event types (mirrors agent headless lifecycle).
const (
	StepEventStart     = "step_start"
	StepEventToolStart = "tool_start"
	StepEventToolEnd   = "tool_end"
	StepEventReasoning = "reasoning"
	StepEventText      = "text"
	StepEventEnd       = "step_end"
)

// StepLifecycleEvent is the automation-facing shape of an agent step
// lifecycle signal, enriched with workflow/run identity for sinks.
type StepLifecycleEvent struct {
	Type           string
	RunID          string
	WorkflowID     string
	JobID          string
	StepID         string
	ConversationID string
	AgentRunID     string
	Round          int
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

const notifyDetailCap = 200

// NotifyProgressSink forwards step lifecycle events to an MCP plugin tool
// (internal_send_progress, falling back to admin.send_progress). Delivery is
// best-effort, throttled to at most one MCP call per tool-call round, and
// never blocks or fails the workflow run.
type NotifyProgressSink struct {
	Caller MCPToolCaller
	Bus    Emitter

	mu       sync.Mutex
	roundBuf map[string]*notifyRoundBuffer // key: runID|stepID|round
	flushed  map[string]bool               // runID|stepID|round already sent
}

type notifyRoundBuffer struct {
	toolName   string
	toolStatus string
	parts      []string
}

func NewNotifyProgressSink(caller MCPToolCaller, bus Emitter) *NotifyProgressSink {
	return &NotifyProgressSink{
		Caller:   caller,
		Bus:      bus,
		roundBuf: map[string]*notifyRoundBuffer{},
		flushed:  map[string]bool{},
	}
}

func (n *NotifyProgressSink) OnStepEvent(ctx context.Context, ev StepLifecycleEvent) error {
	if n == nil || !ev.Notify.Enabled() {
		return nil
	}
	detail := ev.Notify.NormalizedDetail()
	switch ev.Type {
	case StepEventStart:
		return n.deliver(ctx, ev, ev.Type, "running", "")
	case StepEventEnd:
		_ = n.flushPending(ctx, ev)
		status := ev.Status
		if status == "" {
			status = "success"
		}
		return n.deliver(ctx, ev, ev.Type, status, truncateNotify(ev.Error))
	case StepEventToolStart, StepEventToolEnd, StepEventReasoning, StepEventText:
		if !notifyWants(detail, ev.Type) {
			return nil
		}
		return n.bufferAndMaybeFlush(ctx, ev)
	default:
		return nil
	}
}

func notifyWants(detail domain.NotifyDetail, typ string) bool {
	switch detail {
	case domain.NotifyDetailNone:
		return false
	case domain.NotifyDetailTools:
		return typ == StepEventToolStart || typ == StepEventToolEnd || typ == StepEventReasoning || typ == StepEventText
	case domain.NotifyDetailText:
		return typ == StepEventReasoning || typ == StepEventText
	case domain.NotifyDetailAll:
		return true
	default:
		return typ == StepEventToolStart || typ == StepEventToolEnd
	}
}

func (n *NotifyProgressSink) bufferAndMaybeFlush(ctx context.Context, ev StepLifecycleEvent) error {
	key := ev.RunID + "|" + ev.StepID + "|" + strconv.Itoa(ev.Round)
	n.mu.Lock()
	buf := n.roundBuf[key]
	if buf == nil {
		buf = &notifyRoundBuffer{}
		n.roundBuf[key] = buf
	}
	switch ev.Type {
	case StepEventToolStart:
		buf.toolName = ev.ToolName
		if d := strings.TrimSpace(ev.Detail); d != "" {
			buf.parts = append(buf.parts, "args: "+d)
		}
	case StepEventToolEnd:
		buf.toolName = ev.ToolName
		buf.toolStatus = ev.Status
		if d := strings.TrimSpace(ev.Detail); d != "" {
			buf.parts = append(buf.parts, "out: "+d)
		}
	case StepEventReasoning, StepEventText:
		if d := strings.TrimSpace(ev.Detail); d != "" {
			buf.parts = append(buf.parts, d)
		}
	}
	shouldFlush := ev.Type == StepEventToolEnd ||
		(ev.Notify.NormalizedDetail() == domain.NotifyDetailText && (ev.Type == StepEventText || ev.Type == StepEventReasoning))
	if !shouldFlush || n.flushed[key] {
		n.mu.Unlock()
		return nil
	}
	n.flushed[key] = true
	toolName := buf.toolName
	toolStatus := buf.toolStatus
	detail := strings.Join(buf.parts, "\n")
	n.mu.Unlock()

	status := toolStatus
	if status == "" {
		status = "ok"
	}
	_ = toolName
	return n.deliver(ctx, ev, "progress", status, truncateNotify(detail))
}

func (n *NotifyProgressSink) flushPending(ctx context.Context, ev StepLifecycleEvent) error {
	prefix := ev.RunID + "|" + ev.StepID + "|"
	n.mu.Lock()
	var pending []struct {
		key, detail, status string
	}
	for key, buf := range n.roundBuf {
		if !strings.HasPrefix(key, prefix) || n.flushed[key] {
			continue
		}
		if len(buf.parts) == 0 && buf.toolName == "" {
			continue
		}
		n.flushed[key] = true
		status := buf.toolStatus
		if status == "" {
			status = "ok"
		}
		pending = append(pending, struct {
			key, detail, status string
		}{key: key, detail: strings.Join(buf.parts, "\n"), status: status})
	}
	n.mu.Unlock()
	for _, p := range pending {
		_ = n.deliver(ctx, ev, "progress", p.status, truncateNotify(p.detail))
	}
	return nil
}

func (n *NotifyProgressSink) deliver(ctx context.Context, ev StepLifecycleEvent, eventType, status, detail string) error {
	chatID := ""
	if ev.Notify != nil && strings.TrimSpace(ev.Notify.ChatID) != "" {
		chatID = strings.TrimSpace(domain.RenderAgentPrompt(ev.Notify.ChatID, ev.TriggerEvent))
	}
	if chatID == "" {
		n.emitSkipped(ev, "missing_chat_id")
		return nil
	}
	if n.Caller == nil {
		n.emitSkipped(ev, "no_mcp_caller")
		return nil
	}
	plugin := strings.TrimSpace(ev.Notify.Plugin)
	payload := map[string]any{
		"chat_id":    chatID,
		"event_type": eventType,
		"status":     status,
		"title":      truncateNotify(firstNonEmpty(ev.ToolName, eventType, ev.StepID)),
		"detail":     detail,
	}
	if ev.ConversationID != "" {
		payload["message_id"] = ev.ConversationID
	}
	serverID := "plugin:" + strings.TrimPrefix(plugin, "plugin:")
	_, err := n.Caller.CallTool(ctx, serverID, "internal_send_progress", payload)
	if err != nil {
		_, err2 := n.Caller.CallTool(ctx, serverID, "admin.send_progress", payload)
		if err2 != nil {
			n.emitFailed(ev, err.Error()+"; fallback: "+err2.Error())
			return nil
		}
	}
	return nil
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
