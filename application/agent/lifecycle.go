package agent

import (
	"strings"
	"sync"

	"nusashell/application/tools"
	"nusashell/contracts"
	"nusashell/pkg/text"
)

// Lifecycle event types emitted for headless automation turns.
const (
	LifecycleStepStart = "step_start"
	LifecycleToolStart = "tool_start"
	LifecycleToolEnd   = "tool_end"
	LifecycleReasoning = "reasoning"
	LifecycleText      = "text"
	LifecycleStepEnd   = "step_end"
)

const lifecycleDetailCap = 200

// AgentLifecycleEvent is one headless automation turn lifecycle signal.
// Identity fields (RunID, ConversationID) are always populated once the
// turn has a conversation so automation can steer while StatusRunning.
type AgentLifecycleEvent struct {
	Type           string
	RunID          string
	ConversationID string
	Round          int
	ToolName       string
	Status         string // ok|error for tool_end; success|error for step_end
	Detail         string // truncated ≤200
	Error          string
}

// AgentLifecycleListener receives headless automation lifecycle events.
type AgentLifecycleListener func(AgentLifecycleEvent)

// AddAgentLifecycleListener registers a listener for headless automation
// turn lifecycle. Listeners are invoked synchronously from the publish path;
// slow work must be detached by the listener (goSafe).
func (a *Service) AddAgentLifecycleListener(fn AgentLifecycleListener) {
	if a == nil || fn == nil {
		return
	}
	a.lifecycleMu.Lock()
	a.lifecycleListeners = append(a.lifecycleListeners, fn)
	a.lifecycleMu.Unlock()
}

func (a *Service) emitLifecycle(ev AgentLifecycleEvent) {
	if a == nil {
		return
	}
	a.lifecycleMu.Lock()
	listeners := append([]AgentLifecycleListener(nil), a.lifecycleListeners...)
	a.lifecycleMu.Unlock()
	for _, fn := range listeners {
		func(listener AgentLifecycleListener) {
			defer func() { _ = recover() }()
			listener(ev)
		}(fn)
	}
}

// emitHeadlessLifecycle publishes a lifecycle event only for headless
// automation turns (pipeline agent steps). Interactive and learner turns
// stay silent on this channel.
func (a *Service) emitHeadlessLifecycle(run *TurnRun, ev AgentLifecycleEvent) {
	if run == nil || !run.Headless {
		return
	}
	if run.ToolKind != "" && run.ToolKind != tools.AgentAutomation {
		return
	}
	ev.RunID = run.ID
	ev.ConversationID = run.ConversationID
	a.emitLifecycle(ev)
}

func truncateLifecycle(s string) string {
	return text.Truncate(strings.TrimSpace(s), lifecycleDetailCap)
}

// lifecycleRoundGate tracks per-run "one reasoning per round" throttling.
type lifecycleRoundGate struct {
	mu        sync.Mutex
	reasoning map[string]int // runID -> last round that emitted reasoning
}

func (g *lifecycleRoundGate) allowReasoning(runID string, round int) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reasoning == nil {
		g.reasoning = map[string]int{}
	}
	if last, ok := g.reasoning[runID]; ok && last == round {
		return false
	}
	g.reasoning[runID] = round
	return true
}

func (g *lifecycleRoundGate) clear(runID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.reasoning, runID)
	g.mu.Unlock()
}

func (a *Service) lookupRun(runID string) *TurnRun {
	if a == nil || runID == "" {
		return nil
	}
	a.runsMu.Lock()
	defer a.runsMu.Unlock()
	return a.runs[runID]
}

// notifyLifecycleDelta is called from the round-stream publish helpers so
// capture stays single-sourced with SSE round deltas.
func (a *Service) notifyLifecycleDelta(runID, messageID string, round int, kind, toolCallID, name, text string) {
	run := a.lookupRun(runID)
	if run == nil {
		return
	}
	switch kind {
	case contracts.RoundDeltaReasoning:
		if !a.lifecycleGate.allowReasoning(runID, round) {
			return
		}
		a.emitHeadlessLifecycle(run, AgentLifecycleEvent{
			Type:   LifecycleReasoning,
			Round:  round,
			Detail: truncateLifecycle(text),
		})
	case contracts.RoundDeltaText:
		a.emitHeadlessLifecycle(run, AgentLifecycleEvent{
			Type:   LifecycleText,
			Round:  round,
			Detail: truncateLifecycle(text),
		})
	case contracts.RoundDeltaTool:
		// Streaming tool output chunks — ignore for lifecycle (tool_end carries status).
		_ = toolCallID
		_ = messageID
		_ = name
	}
}

func (a *Service) notifyLifecycleToolStart(runID string, round int, name, args string) {
	run := a.lookupRun(runID)
	if run == nil {
		return
	}
	a.emitHeadlessLifecycle(run, AgentLifecycleEvent{
		Type:     LifecycleToolStart,
		Round:    round,
		ToolName: name,
		Detail:   truncateLifecycle(args),
	})
}

func (a *Service) notifyLifecycleToolEnd(run *TurnRun, round int, name string, status string, output string) {
	a.emitHeadlessLifecycle(run, AgentLifecycleEvent{
		Type:     LifecycleToolEnd,
		Round:    round,
		ToolName: name,
		Status:   status,
		Detail:   truncateLifecycle(output),
	})
}

func (a *Service) notifyLifecycleStepStart(run *TurnRun) {
	a.emitHeadlessLifecycle(run, AgentLifecycleEvent{Type: LifecycleStepStart})
}

func (a *Service) notifyLifecycleStepEnd(run *TurnRun, status, errMsg string) {
	a.emitHeadlessLifecycle(run, AgentLifecycleEvent{
		Type:   LifecycleStepEnd,
		Status: status,
		Error:  truncateLifecycle(errMsg),
	})
	if run != nil {
		a.lifecycleGate.clear(run.ID)
	}
}
