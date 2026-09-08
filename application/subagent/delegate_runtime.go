package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// delegateRun is one internal delegate run: the public ACP-shaped snapshot
// plus the cancellation handle, completion signal, and hidden conversation
// id that stop/wait/steer need.
type delegateRun struct {
	run       *domain.AcpRun
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan *domain.AcpRun
	runConvID string
}

// DelegateRuntime executes internal NusaShell delegate runs on the shared
// headless turn runner and exposes them through the same Runtime surface as
// ACP subagent sessions, so steer/stop/wait/get/list work identically for
// both families.
type DelegateRuntime struct {
	svc *Service

	mu   sync.Mutex
	runs map[string]*delegateRun
}

func newDelegateRuntime(svc *Service) *DelegateRuntime {
	return &DelegateRuntime{svc: svc, runs: map[string]*delegateRun{}}
}

// DelegateRunSnapshot returns a clone of the delegate run, or ok=false.
func (s *Service) DelegateRunSnapshot(runID string) (*domain.AcpRun, bool) {
	if s == nil || s.delegates == nil {
		return nil, false
	}
	return s.delegates.Get(runID)
}

var errDelegateRunNotFound = errors.New("acp run not found")

// Spawn registers the run, starts the headless turn in a managed goroutine,
// and returns immediately — internal delegates are always async.
func (r *DelegateRuntime) Spawn(_ context.Context, req SpawnRequest) (*domain.AcpRun, error) {
	runID := domain.NewID(domain.IDPrefixRun)
	now := clock.NewTime().Time()
	ctx, cancel := context.WithCancel(context.Background())
	_, currentModel, ok := domain.SplitQualifiedModel(strings.TrimSpace(req.ModelID))
	if !ok {
		currentModel = strings.TrimSpace(req.ModelID)
	}
	dr := &delegateRun{
		run: &domain.AcpRun{
			TaskState: domain.TaskState[domain.AcpRunStatus]{
				ID:        runID,
				Status:    domain.AcpRunStarting,
				StartedAt: now,
			},
			AgentID:          internalDelegateAgentID,
			AgentName:        internalDelegateAgentName,
			Title:            domain.NormalizeAcpRunTitle(req.Title),
			ConversationID:   req.ConversationID,
			ParentToolCallID: req.ParentToolCallID,
			Workspace:        req.Workspace,
			Prompt:           req.Prompt,
			CurrentModelID:   currentModel,
			RiskTier:         domain.TrustLevelToRiskTierCap(domain.TrustTrusted),
			UpdatedAt:        now,
		},
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan *domain.AcpRun, 1),
	}
	r.mu.Lock()
	r.runs[runID] = dr
	dr.run.BeginRunning(clock.NewTime().Time())
	snapshot := cloneDelegateRun(dr.run)
	r.mu.Unlock()

	r.svc.EmitRun(contracts.EventAcpRunStarted, snapshot)
	r.svc.trackPending(req.ConversationID, runID, domain.SubagentToolName)
	r.svc.goSafe("delegate", func() { r.exec(runID, dr, req) })
	return snapshot, nil
}

// Steer queues a steer message on the delegate's running headless turn.
func (r *DelegateRuntime) Steer(runID, text string) error {
	r.mu.Lock()
	dr := r.runs[runID]
	live := dr != nil && dr.run.Live()
	convID := ""
	if dr != nil {
		convID = dr.runConvID
	}
	r.mu.Unlock()
	if dr == nil || !live {
		return errDelegateRunNotFound
	}
	if convID == "" {
		return fmt.Errorf("internal delegate turn is not active")
	}
	if r.svc.deps.SteerHeadless == nil {
		return fmt.Errorf("steer is not configured")
	}
	return r.svc.deps.SteerHeadless(convID, text)
}

// Stop cancels the delegate's headless turn and waits for the run to reach
// a terminal state so the caller observes the cancellation.
func (r *DelegateRuntime) Stop(runID string) error {
	r.mu.Lock()
	dr := r.runs[runID]
	live := dr != nil && dr.run.Live()
	r.mu.Unlock()
	if dr == nil {
		return errDelegateRunNotFound
	}
	if live {
		dr.cancel()
		select {
		case <-dr.done:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

// Wait blocks until the run is terminal or ctx expires.
func (r *DelegateRuntime) Wait(ctx context.Context, runID string) (*domain.AcpRun, error) {
	r.mu.Lock()
	dr := r.runs[runID]
	var snapshot *domain.AcpRun
	if dr != nil {
		snapshot = cloneDelegateRun(dr.run)
	}
	r.mu.Unlock()
	if dr == nil {
		return nil, errDelegateRunNotFound
	}
	if !snapshot.Live() {
		return snapshot, nil
	}
	select {
	case run := <-dr.done:
		return run, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *DelegateRuntime) Get(runID string) (*domain.AcpRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dr := r.runs[runID]
	if dr == nil {
		return nil, false
	}
	return cloneDelegateRun(dr.run), true
}

func (r *DelegateRuntime) List(conversationID string) []*domain.AcpRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.AcpRun, 0, len(r.runs))
	for _, dr := range r.runs {
		if conversationID != "" && dr.run.ConversationID != conversationID {
			continue
		}
		out = append(out, cloneDelegateRun(dr.run))
	}
	return out
}

// Close cancels every live delegate run during shutdown.
func (r *DelegateRuntime) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, dr := range r.runs {
		if dr.run.Live() {
			dr.cancel()
		}
	}
}

func (r *DelegateRuntime) Probe(context.Context, *domain.AcpAgent) (domain.AcpAgent, error) {
	return domain.AcpAgent{}, errUnsupportedDelegateOp
}

func (r *DelegateRuntime) Authenticate(context.Context, *domain.AcpAgent, string) error {
	return errUnsupportedDelegateOp
}

func (r *DelegateRuntime) RefreshCatalog(context.Context, *domain.AcpAgent) (domain.AcpAgent, error) {
	return domain.AcpAgent{}, errUnsupportedDelegateOp
}

func (r *DelegateRuntime) DecidePermission(string, string, string, domain.PermissionOutcome) error {
	return errUnsupportedDelegateOp
}

func (r *DelegateRuntime) PromoteRisk(string, domain.RiskTier) error {
	return errUnsupportedDelegateOp
}

func (r *DelegateRuntime) SetMode(context.Context, string, string) error {
	return errUnsupportedDelegateOp
}

var errUnsupportedDelegateOp = errors.New("not supported for internal delegate runs")

// exec runs the headless turn to completion and seals the run.
func (r *DelegateRuntime) exec(runID string, dr *delegateRun, req SpawnRequest) {
	out, runConvID, err := r.runHeadless(dr, req)
	text := ""
	if out != nil {
		if v, ok := out["output"].(string); ok {
			text = v
		}
	}
	r.finish(runID, dr, runConvID, text, err)
}

func (r *DelegateRuntime) runHeadless(dr *delegateRun, req SpawnRequest) (output map[string]any, runConvID string, err error) {
	headless := r.svc.deps.Headless
	if headless == nil {
		return nil, "", fmt.Errorf("internal delegation runner is not wired")
	}
	return headless(dr.ctx, req.Prompt, req.ModelID, domain.TrustTrusted, nil, func(conversationID string) {
		r.attachConversation(dr.run.ID, conversationID)
	})
}

// attachConversation records the hidden conversation id and refreshes the
// live transcript while the headless turn progresses.
func (r *DelegateRuntime) attachConversation(runID, conversationID string) {
	if conversationID == "" {
		return
	}
	r.mu.Lock()
	dr := r.runs[runID]
	if dr == nil {
		r.mu.Unlock()
		return
	}
	if dr.runConvID == "" {
		dr.runConvID = conversationID
	}
	live := dr.run.Live()
	r.mu.Unlock()
	if !live || r.svc.deps.Conversations == nil {
		return
	}
	conversation, err := r.svc.deps.Conversations.Get(conversationID)
	if err != nil {
		return
	}
	transcript := delegateTranscriptFromConversation(conversation)
	r.mu.Lock()
	dr = r.runs[runID]
	if dr == nil || !dr.run.Live() {
		r.mu.Unlock()
		return
	}
	dr.run.Transcript = transcript
	dr.run.UpdatedAt = clock.NewTime().Time()
	snapshot := cloneDelegateRun(dr.run)
	r.mu.Unlock()
	r.svc.EmitRun(contracts.EventAcpRunUpdated, snapshot)
}

// finish seals the public snapshot after the hidden headless conversation
// has completed, then delivers the result through the shared subagent
// completion path.
func (r *DelegateRuntime) finish(runID string, dr *delegateRun, runConvID, output string, runErr error) {
	now := clock.NewTime().Time()
	r.mu.Lock()
	run := dr.run
	if runConvID != "" && r.svc.deps.Conversations != nil {
		if conversation, err := r.svc.deps.Conversations.Get(runConvID); err == nil {
			run.Transcript = delegateTranscriptFromConversation(conversation)
		}
	}
	if len(run.Transcript) == 0 && strings.TrimSpace(output) != "" {
		run.AppendTranscript(domain.AcpTranscriptChunk{Kind: "text", Text: output, At: now})
	}
	status := domain.AcpRunCompleted
	errText := ""
	stopReason := "completed"
	switch {
	case dr.ctx.Err() != nil:
		status = domain.AcpRunCancelled
		stopReason = "cancelled"
		run.AppendTranscript(domain.AcpTranscriptChunk{Kind: "status", Text: "Delegate cancelled.", At: now})
	case runErr != nil:
		status = domain.AcpRunFailed
		errText = runErr.Error()
		stopReason = "error"
		run.AppendTranscript(domain.AcpTranscriptChunk{Kind: "status", Text: "Delegate failed: " + errText, At: now})
	}
	run.Finish(status, errText, stopReason, now)
	snapshot := cloneDelegateRun(run)
	r.mu.Unlock()

	select {
	case dr.done <- snapshot:
	default:
	}
	r.svc.OnRunDone(snapshot)
}
