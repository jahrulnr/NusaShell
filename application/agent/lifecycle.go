package agent

import (
	"context"
	"strings"

	"nusashell/application/tools"
	"nusashell/pkg/text"
)

// Agent lifecycle event kinds (observer registry). Side-effect observers only —
// no veto/control hooks (those would be a separate middleware mechanism).
const (
	AgentEventRun       = "run"
	AgentEventStep      = "step"
	AgentEventToolCall  = "tool_call"
	AgentEventReasoning = "reasoning"
	AgentEventText      = "text"
)

// Agent lifecycle phases pair pre/post for run, step, and tool_call.
const (
	AgentPhasePre  = "pre"
	AgentPhasePost = "post"
)

// Observer status values for tool_call / step / run post events.
const (
	AgentStatusRunning = "running"
	AgentStatusOK      = "ok"
	AgentStatusError   = "error"
)

const lifecycleDetailCap = 200

// AgentLifecycleEvent is one headless automation turn lifecycle signal
// dispatched to registered AgentObservers in FIFO order.
type AgentLifecycleEvent struct {
	Kind           string // run|step|tool_call|reasoning|text
	Phase          string // pre|post (reasoning/text are post-only)
	RunID          string
	ConversationID string
	Round          int
	CallID         string // domain tool call ID; required to pair parallel tool_call events
	ToolName       string
	Status         string // running|ok|error
	Detail         string // truncated ≤200
	Error          string
}

// AgentObserver receives headless automation lifecycle events. Observers are
// side-effect only (no veto). Invoked synchronously in registration order;
// slow work must detach (goSafe) inside the observer.
type AgentObserver interface {
	OnAgentEvent(ctx context.Context, ev AgentLifecycleEvent)
}

// AgentObserverFunc adapts a function to AgentObserver.
type AgentObserverFunc func(ctx context.Context, ev AgentLifecycleEvent)

// OnAgentEvent implements AgentObserver.
func (f AgentObserverFunc) OnAgentEvent(ctx context.Context, ev AgentLifecycleEvent) {
	if f != nil {
		f(ctx, ev)
	}
}

// RegisterAgentObserver appends an observer. Observers run FIFO on each event.
func (a *Service) RegisterAgentObserver(o AgentObserver) {
	if a == nil || o == nil {
		return
	}
	a.lifecycleMu.Lock()
	a.observers = append(a.observers, o)
	a.lifecycleMu.Unlock()
}

func (a *Service) dispatchObservers(ctx context.Context, ev AgentLifecycleEvent) {
	if a == nil {
		return
	}
	a.lifecycleMu.Lock()
	observers := append([]AgentObserver(nil), a.observers...)
	a.lifecycleMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	for _, o := range observers {
		func(obs AgentObserver) {
			defer func() { _ = recover() }()
			obs.OnAgentEvent(ctx, ev)
		}(o)
	}
}

func isHeadlessAutomationRun(run *TurnRun) bool {
	if run == nil || !run.Headless {
		return false
	}
	return run.ToolKind == "" || run.ToolKind == tools.AgentAutomation
}

// emitHeadlessEvent publishes one lifecycle event for headless automation
// turns only. Interactive and learner turns stay silent on this channel.
func (a *Service) emitHeadlessEvent(run *TurnRun, ev AgentLifecycleEvent) {
	if !isHeadlessAutomationRun(run) {
		return
	}
	ev.RunID = run.ID
	ev.ConversationID = run.ConversationID
	ctx := context.Background()
	if run.Ctx != nil {
		ctx = run.Ctx
	}
	a.dispatchObservers(ctx, ev)
}

func truncateLifecycle(s string) string {
	return text.Truncate(strings.TrimSpace(s), lifecycleDetailCap)
}

// emitRunStepPre fires run+step pre at the headless turn boundary.
func (a *Service) emitRunStepPre(run *TurnRun) {
	a.emitHeadlessEvent(run, AgentLifecycleEvent{
		Kind: AgentEventRun, Phase: AgentPhasePre, Status: AgentStatusRunning,
	})
	a.emitHeadlessEvent(run, AgentLifecycleEvent{
		Kind: AgentEventStep, Phase: AgentPhasePre, Status: AgentStatusRunning,
	})
}

// emitRunStepPost fires step+run post at the headless turn boundary.
func (a *Service) emitRunStepPost(run *TurnRun, status, errMsg string) {
	st := AgentStatusOK
	if status == "error" || status == AgentStatusError {
		st = AgentStatusError
	}
	errDetail := truncateLifecycle(errMsg)
	a.emitHeadlessEvent(run, AgentLifecycleEvent{
		Kind: AgentEventStep, Phase: AgentPhasePost, Status: st, Error: errDetail,
	})
	a.emitHeadlessEvent(run, AgentLifecycleEvent{
		Kind: AgentEventRun, Phase: AgentPhasePost, Status: st, Error: errDetail,
	})
}

// emitToolCallPre fires tool_call pre (placeholder open) for one call.
func (a *Service) emitToolCallPre(run *TurnRun, round int, call domainToolCallRef) {
	a.emitHeadlessEvent(run, AgentLifecycleEvent{
		Kind:     AgentEventToolCall,
		Phase:    AgentPhasePre,
		Round:    round,
		CallID:   call.ID,
		ToolName: call.Name,
		Status:   AgentStatusRunning,
		Detail:   truncateLifecycle(call.Args),
	})
}

// emitToolCallPost fires tool_call post with final status for one call.
func (a *Service) emitToolCallPost(run *TurnRun, round int, call domainToolCallRef, status, output string) {
	a.emitHeadlessEvent(run, AgentLifecycleEvent{
		Kind:     AgentEventToolCall,
		Phase:    AgentPhasePost,
		Round:    round,
		CallID:   call.ID,
		ToolName: call.Name,
		Status:   status,
		Detail:   truncateLifecycle(output),
	})
}

// emitRoundContentObservers emits reasoning and text once per round (post)
// from the completed provider response — not from mid-stream deltas.
func (a *Service) emitRoundContentObservers(run *TurnRun, round int, reasoning, content string) {
	if r := strings.TrimSpace(reasoning); r != "" {
		a.emitHeadlessEvent(run, AgentLifecycleEvent{
			Kind: AgentEventReasoning, Phase: AgentPhasePost, Round: round,
			Detail: truncateLifecycle(r),
		})
	}
	if t := strings.TrimSpace(content); t != "" {
		a.emitHeadlessEvent(run, AgentLifecycleEvent{
			Kind: AgentEventText, Phase: AgentPhasePost, Round: round,
			Detail: truncateLifecycle(t),
		})
	}
}

// domainToolCallRef is the minimal tool-call identity used at emission sites.
type domainToolCallRef struct {
	ID, Name, Args string
}
