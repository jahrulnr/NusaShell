package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	jobLease     = domain.JobLease
	jobHeartbeat = domain.JobHeartbeat
	maxParallel  = domain.MaxParallelJobs
	maxFanout    = domain.MaxFanout
)

// ExecutionScheduler evaluates the DAG, matches runners, and runs jobs.
type ExecutionScheduler struct {
	Runs     PipelineRunStore
	Logs     ExecutionLogStore
	Exec     JobExecutor
	Caps     CapabilityResolver
	Agent    AgentStepRunner
	Convs    ConversationKeyStore
	Runners  RunnerRegistry
	Waits    WaitStore
	Bus      Emitter
	Clock    Clock
	MaxJobs  int
	Notifier RunNotifier
	Go       func(source string, fn func())

	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
	cancelOwner map[string]string
	runMu       sync.Map
	convMu      sync.Map // conversation key -> chan struct{} (reuse guard)
}

func NewExecutionScheduler() *ExecutionScheduler {
	return &ExecutionScheduler{cancels: map[string]context.CancelFunc{}, cancelOwner: map[string]string{}, MaxJobs: maxParallel, Clock: SystemClock{}}
}

func (s *ExecutionScheduler) lockRun(id string) *sync.Mutex {
	v, _ := s.runMu.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *ExecutionScheduler) now() time.Time {
	if s.Clock == nil {
		return clock.NewTime().Time()
	}
	return clock.NewTime(s.Clock.Now()).Time()
}

// goSafe dispatches detached scheduler work through the application lifecycle
// boundary when the composition root provides one. The local fallback keeps
// package-level tests and partial wiring panic-contained as well.
func (s *ExecutionScheduler) goSafe(source string, fn func()) {
	if s != nil && s.Go != nil {
		s.Go(source, fn)
		return
	}
	go func() {
		defer func() { _ = recover() }()
		fn()
	}()
}

// tryLockConversationKey serializes agent steps that reuse the same
// conversation key across overlapping runs inside this process. Overlapping
// writes to one transcript would corrupt the agent's memory, so the second
// run fails its step with a visible "busy" error instead of interleaving.
// The guard is process-local; cross-process overlap is prevented by the
// workflow-level concurrency lock the scheduler already enforces.
func (s *ExecutionScheduler) tryLockConversationKey(key string) (func(), bool) {
	value, _ := s.convMu.LoadOrStore(key, make(chan struct{}, 1))
	ch := value.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}

type runStartFailure struct {
	created bool
	err     error
}

func (e *runStartFailure) Error() string {
	if e == nil || e.err == nil {
		return "workflow run start failed"
	}
	return e.err.Error()
}

func (e *runStartFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// StartRun persists a snapshot and begins scheduling.
func (s *ExecutionScheduler) StartRun(ctx context.Context, run *domain.WorkflowRun) error {
	if s == nil || s.Runs == nil {
		return fmt.Errorf("run store not configured")
	}
	if run == nil {
		return fmt.Errorf("workflow run is empty")
	}
	run.StartRun(s.now())
	if err := s.Runs.Create(ctx, run); err != nil {
		return fmt.Errorf("create workflow run: %w", &runStartFailure{err: err})
	}
	s.emit(contracts.EventAutomationRunCreated, map[string]any{"run_id": run.ID, "workflow_id": run.WorkflowID, "status": run.Status})
	if err := s.Tick(ctx, run.ID); err != nil {
		return fmt.Errorf("schedule workflow run: %w", &runStartFailure{created: true, err: err})
	}
	return nil
}

// StartRunAsync persists a snapshot, then begins scheduling in a background
// goroutine with a detached context. The caller receives the run ID
// immediately and can poll status with automation(op="status") or block with automation(op="wait").
// Cancel still works: it sets terminal status, which the Tick loop observes
// on its next iteration.
func (s *ExecutionScheduler) StartRunAsync(ctx context.Context, run *domain.WorkflowRun) error {
	if s == nil || s.Runs == nil {
		return fmt.Errorf("run store not configured")
	}
	if run == nil {
		return fmt.Errorf("workflow run is empty")
	}
	run.StartRun(s.now())
	if err := s.Runs.Create(ctx, run); err != nil {
		return fmt.Errorf("create workflow run: %w", &runStartFailure{err: err})
	}
	s.emit(contracts.EventAutomationRunCreated, map[string]any{"run_id": run.ID, "workflow_id": run.WorkflowID, "status": run.Status})
	s.goSafe("automation-run", func() { s.runTickAsync(run.ID) })
	return nil
}

// runTickAsync is the recovery boundary for the detached scheduler worker.
// A panic here cannot be handled by runJob because it may happen while
// loading the run or evaluating scheduler state. Preserve a durable terminal
// state when possible, and never allow failure reporting to panic recursively.
func (s *ExecutionScheduler) runTickAsync(runID string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			reason := fmt.Sprintf("automation run %q scheduler panicked: %v", runID, recovered)
			s.recordAsyncFailure(runID, reason)
		}
	}()
	if err := s.Tick(context.Background(), runID); err != nil {
		// A background error is otherwise invisible because there is no caller
		// to receive it. Keep the run observable by recording a terminal
		// failure, while preserving the original diagnostic in the lifecycle
		// event so operators can choose an explicit rerun/manual recovery.
		s.recordAsyncFailure(runID, fmt.Sprintf("background scheduler: %v", err))
	}
}

func (s *ExecutionScheduler) recordAsyncFailure(runID, reason string) {
	defer func() { _ = recover() }()
	if s == nil || s.Runs == nil {
		return
	}
	run, err := s.Runs.Get(context.Background(), runID)
	if err != nil {
		s.emit(contracts.EventAutomationRunFailed, map[string]any{
			"run_id": runID, "error": fmt.Sprintf("%s; failed to load run: %v", reason, err),
		})
		return
	}
	if run == nil {
		s.emit(contracts.EventAutomationRunFailed, map[string]any{
			"run_id": runID, "error": reason + "; run store returned an empty run",
		})
		return
	}
	if run.Status.IsTerminal() {
		return
	}
	failedAt := s.now()
	run.FailDAG(failedAt)
	for i := range run.Jobs {
		if !run.Jobs[i].Status.IsTerminal() {
			run.Jobs[i].Fail(reason, failedAt)
		}
	}
	if err := s.persist(context.Background(), run); err != nil {
		reason = fmt.Sprintf("%s; failed to persist terminal state: %v", reason, err)
	}
	s.emit(contracts.EventAutomationRunFailed, map[string]any{"run_id": runID, "error": reason})
	s.notifyWebhook(context.Background(), run)
}

// Tick re-evaluates one run.
func (s *ExecutionScheduler) Tick(ctx context.Context, runID string) error {
	if s == nil || s.Runs == nil {
		return fmt.Errorf("run store not configured")
	}
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("run ID is required")
	}
	run, err := s.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status.IsTerminal() {
		return nil
	}
	if run.Status == domain.StatusWaiting {
		if run.WakeAt != nil && s.now().Before(*run.WakeAt) {
			return nil
		}
		domain.WakeWaitingRun(run)
		if err := s.persist(ctx, run); err != nil {
			return err
		}
	}
	if run.Status == domain.StatusBlocked {
		return nil
	}
	for {
		run, err = s.Runs.Get(ctx, runID)
		if err != nil {
			return err
		}
		if run.Status.IsTerminal() || run.Status == domain.StatusBlocked {
			return nil
		}
		dag, issues := domain.BuildDAG(run.Definition.Jobs)
		if len(issues) > 0 {
			run.FailDAG(s.now())
			if persistErr := s.persist(ctx, run); persistErr != nil {
				return errors.Join(fmt.Errorf("%s", issues[0].Message), fmt.Errorf("persist failed run state: %w", persistErr))
			}
			s.emit(contracts.EventAutomationRunFailed, map[string]any{"run_id": run.ID, "error": issues[0].Message})
			s.notifyWebhook(ctx, run)
			return fmt.Errorf("%s", issues[0].Message)
		}
		status := map[string]domain.RunStatus{}
		continueOn := map[string]bool{}
		for i := range run.Jobs {
			status[run.Jobs[i].JobID] = run.Jobs[i].Status
			if j := run.Definition.JobByID(run.Jobs[i].JobID); j != nil {
				continueOn[run.Jobs[i].JobID] = j.ContinueOnError
			}
		}
		ready := domain.ReadyJobs(dag, status, continueOn)
		if len(ready) == 0 {
			return s.maybeFinalize(ctx, run)
		}
		claimed := domain.ClaimJobs(run, ready)
		if err := s.persist(ctx, run); err != nil {
			return err
		}
		if len(claimed) == 0 {
			return s.maybeFinalize(ctx, run)
		}
		n := s.MaxJobs
		if n <= 0 {
			n = maxParallel
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, n)
		jobErrors := make(chan error, len(claimed))
		for _, jobID := range claimed {
			jobID := jobID
			wg.Add(1)
			sem <- struct{}{}
			s.goSafe("automation-job", func() {
				defer wg.Done()
				defer func() { <-sem }()
				if err := s.runJob(ctx, runID, jobID); err != nil {
					jobErrors <- fmt.Errorf("job %q: %w", jobID, err)
				}
			})
		}
		wg.Wait()
		close(jobErrors)
		var failures []error
		for err := range jobErrors {
			failures = append(failures, err)
		}
		if len(failures) > 0 {
			return errors.Join(failures...)
		}
	}
}

func (s *ExecutionScheduler) runJob(ctx context.Context, runID, jobID string) (err error) {
	var run *domain.WorkflowRun
	var jr *domain.JobRun
	defer func() {
		if recovered := recover(); recovered != nil {
			reason := fmt.Sprintf("automation job %q panicked: %v", jobID, recovered)
			err = fmt.Errorf("%s", reason)
			if run == nil || jr == nil {
				return
			}
			// Failure handling uses the same persistence/event path as ordinary
			// step errors. Keep a second guard here because a broken store or
			// emitter must not turn recovery itself back into a process panic.
			func() {
				defer func() {
					if failurePanic := recover(); failurePanic != nil {
						err = fmt.Errorf("%s; recording panic failure also panicked: %v", err, failurePanic)
					}
				}()
				if failErr := s.failJob(ctx, run, jr, reason); failErr != nil {
					err = fmt.Errorf("%s; failed to persist panic status: %w", err, failErr)
				}
			}()
		}
	}()

	run, err = s.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	jr = run.JobRunByID(jobID)
	job := run.Definition.JobByID(jobID)
	if jr == nil || job == nil {
		return fmt.Errorf("unknown job %s", jobID)
	}
	if jr.Status != domain.StatusQueued && jr.Status != domain.StatusPending && jr.Status != domain.StatusRunning {
		return nil
	}
	ok, err := domain.EvalIf(job.If, domain.BuildConditionEnv(run))
	if err != nil {
		return s.failJob(ctx, run, jr, err.Error())
	}
	if !ok {
		jr.Skip(s.now())
		return s.persist(ctx, run)
	}
	t := s.now()
	jr.BeginRunning(t)
	run.BeginRunning(t)
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventAutomationJobStarted, map[string]any{"run_id": run.ID, "job_id": jobID})

	jobCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels[jr.ID] = cancel
	s.cancelOwner[jr.ID] = runID
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancels, jr.ID)
		delete(s.cancelOwner, jr.ID)
		s.mu.Unlock()
	}()
	if job.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		jobCtx, timeoutCancel = context.WithTimeout(jobCtx, job.Timeout)
		defer timeoutCancel()
	}

	ws, err := s.Exec.Prepare(jobCtx, PrepareRequest{Run: run, Job: *job, JobRun: jr, Workspace: run.Workspace})
	if err != nil {
		return s.failJob(ctx, run, jr, err.Error())
	}
	defer func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.emit("automation.job-cleanup.failed", map[string]any{
					"run_id": run.ID, "job_id": jr.JobID, "error": fmt.Sprintf("cleanup panicked: %v", recovered),
				})
			}
		}()
		if err := s.Exec.Cleanup(context.Background(), CleanupRequest{Workspace: ws}); err != nil {
			s.emit("automation.job-cleanup.failed", map[string]any{
				"run_id": run.ID, "job_id": jr.JobID, "error": err.Error(),
			})
		}
	}()

	outputs := map[string]any{}
	for i := range job.Steps {
		step := job.Steps[i]
		sr := jr.EnsureStep(step)
		if sr.Status.IsTerminal() {
			continue // finished before a wait_until pause; never re-run side effects
		}
		if step.WaitUntil != nil {
			if s.now().Before(*step.WaitUntil) {
				return s.parkWait(ctx, run, jr, sr, step)
			}
		}
		sr.BeginRunning(s.now())
		if err := s.persist(ctx, run); err != nil {
			return err
		}
		s.emit(contracts.EventAutomationStepStarted, map[string]any{"run_id": run.ID, "job_id": jobID, "step_id": sr.ID})

		var result StepResult
		switch {
		case step.WaitUntil != nil:
			result = StepResult{ExitCode: 0}
		case step.Uses != "":
			result, err = s.runUses(jobCtx, run, step)
		case step.Agent != nil:
			if s.Agent == nil {
				result, err = StepResult{Error: "agent steps are not configured"}, fmt.Errorf("agent steps are not configured")
				break
			}
			prompt := domain.RenderAgentPrompt(step.Agent.Prompt, run.Event)
			convKey := ""
			conversationID := ""
			releaseConv := func() {}
			if step.Agent.Reuse {
				convKey = run.WorkflowID
				if step.Agent.Conversation != "" {
					if rendered := domain.RenderConversationKey(step.Agent.Conversation, run.Event); rendered != "" {
						convKey = rendered
					}
				}
				if release, ok := s.tryLockConversationKey(run.WorkflowID + "\x00" + convKey); !ok {
					result, err = StepResult{Error: fmt.Sprintf("conversation key %q is busy (another run is using it); retry after it finishes", convKey)}, fmt.Errorf("conversation key %q is busy", convKey)
					break
				} else {
					releaseConv = release
				}
				defer releaseConv()
				if s.Convs != nil {
					if existing, ok, cerr := s.Convs.GetConversation(context.WithoutCancel(jobCtx), run.WorkflowID, convKey); cerr == nil && ok {
						conversationID = existing
					}
				}
			}
			type agentOutcome struct {
				out    map[string]any
				convID string
				err    error
			}
			agentDone := make(chan agentOutcome, 1)
			go func() {
				outcome := agentOutcome{}
				defer func() {
					if r := recover(); r != nil {
						outcome.err = fmt.Errorf("agent step panicked: %v", r)
					}
					agentDone <- outcome
				}()
				outcome.out, outcome.convID, outcome.err = s.Agent.RunAgentStep(jobCtx, prompt, step.Agent.Model, run.Definition.Trust, step.Agent.OutputSchema, conversationID)
			}()
			recordConversation := func(convID string) {
				if convID == "" || convKey == "" || conversationID != "" || s.Convs == nil {
					return
				}
				if serr := s.Convs.SetConversation(context.WithoutCancel(jobCtx), run.WorkflowID, convKey, convID); serr != nil {
					s.emit("automation.step-conversation-map.failed", map[string]any{
						"run_id": run.ID, "job_id": jobID, "step_id": sr.ID, "error": serr.Error(),
					})
				}
			}
			select {
			case o := <-agentDone:
				if o.err != nil {
					// Preserve the pre-watchdog contract: a panicking agent
					// runner surfaces through runJob's own panic recovery so
					// the run is failed with the diagnostic and the error is
					// returned to the caller. Ordinary agent-step errors keep
					// flowing through the step finalize path below.
					if strings.HasPrefix(o.err.Error(), "agent step panicked: ") {
						panic(o.err)
					}
					recordConversation(o.convID)
					result, err = StepResult{Error: o.err.Error()}, o.err
					break
				}
				sr.ConversationID = o.convID
				recordConversation(o.convID)
				result, err = StepResult{ExitCode: 0, Outputs: o.out}, nil
			case <-jobCtx.Done():
				// Never leave the run eternally "running": when the job
				// context ends (cancel, timeout, shutdown) while the agent
				// turn has not returned, finalize the step and job with a
				// visible reason. The worker goroutine may still finish
				// later; its send is buffered so it cannot block, and the
				// terminal-state guards make any late write a no-op. The
				// terminal persist uses a detached context because the
				// cancelled context would reject the write.
				reason := "agent step did not finish before its context ended"
				if cause := jobCtx.Err(); cause != nil {
					reason = fmt.Sprintf("agent step interrupted: %v", cause)
				}
				detached := context.WithoutCancel(jobCtx)
				sr.Fail(1, reason, s.now())
				s.emit("automation.step.interrupted", map[string]any{
					"run_id": run.ID, "job_id": jobID, "step_id": sr.ID, "reason": reason,
				})
				if ferr := s.failJob(detached, run, jr, reason); ferr != nil {
					return fmt.Errorf("%s; persist failed state: %w", reason, ferr)
				}
				return nil
			}
		default:
			envMap := domain.MergeEnv(run.Definition.Env, job.Env, step.Env)
			envMap["NUSASHELL"] = "true"
			envMap["NUSASHELL_AUTOMATION"] = "true"
			envMap["NUSASHELL_PIPELINE_ID"] = run.WorkflowID
			envMap["NUSASHELL_RUN_ID"] = run.ID
			envMap["NUSASHELL_JOB_ID"] = jr.JobID
			envMap["NUSASHELL_STEP_ID"] = sr.StepID
			envMap["NUSASHELL_WORKSPACE"] = ws.Dir
			result, err = s.Exec.RunStep(jobCtx, RunStepRequest{
				Run: run, Job: *job, JobRun: jr, Step: step, StepRun: sr, Workspace: ws, Env: envMap,
				OnOutput: func(chunk domain.LogChunk) {
					if s.Logs != nil {
						if err := s.Logs.Append(context.Background(), chunk); err != nil {
							s.emit("automation.step-log.failed", map[string]any{
								"run_id": run.ID, "job_id": jr.JobID, "step_id": sr.StepID, "error": err.Error(),
							})
						}
					}
					s.emit(contracts.EventAutomationStepOutput, chunk)
				},
			})
		}
		ft := s.now()
		if err != nil || result.ExitCode != 0 || result.Error != "" {
			errMsg := result.Error
			if err != nil && errMsg == "" {
				errMsg = err.Error()
			}
			sr.Fail(result.ExitCode, errMsg, ft)
			return s.failJob(ctx, run, jr, sr.Error)
		}
		sr.Succeed(result.ExitCode, ft)
		if step.Agent != nil && result.Outputs != nil {
			if text, ok := result.Outputs["output"].(string); ok {
				sr.Output = text
			}
		}
		for k, v := range result.Outputs {
			outputs[k] = v
		}
		if err := s.persist(ctx, run); err != nil {
			return err
		}
		s.emit(contracts.EventAutomationStepCompleted, map[string]any{"run_id": run.ID, "job_id": jobID, "step_id": sr.ID})
	}
	jr.Succeed(outputs, s.now())
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventAutomationJobCompleted, map[string]any{"run_id": run.ID, "job_id": jobID})
	return nil
}

func (s *ExecutionScheduler) parkWait(ctx context.Context, run *domain.WorkflowRun, jr *domain.JobRun, sr *domain.StepRun, step domain.Step) error {
	sr.ParkWait(*step.WaitUntil)
	jr.ParkWait()
	run.ParkWait(*step.WaitUntil)
	if s.Waits != nil {
		if err := s.Waits.Put(ctx, &domain.WaitRecord{
			ID: domain.NewID(domain.IDPrefixWait), WorkflowRunID: run.ID, JobID: jr.JobID, StepID: sr.StepID,
			WakeAt: step.WaitUntil, Status: domain.SchedulePending,
		}); err != nil {
			return fmt.Errorf("persist wait: %w", err)
		}
	}
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventAutomationRunWaiting, map[string]any{"run_id": run.ID, "wake_at": step.WaitUntil})
	return nil
}

func (s *ExecutionScheduler) runUses(ctx context.Context, run *domain.WorkflowRun, step domain.Step) (StepResult, error) {
	if s.Caps == nil {
		return StepResult{Error: "no capability resolver"}, fmt.Errorf("no capability resolver")
	}
	policy := domain.DefaultAutoStart
	for _, t := range run.Definition.Triggers {
		if t.AutoStart != "" {
			policy = t.AutoStart
			break
		}
	}
	binding, err := s.Caps.Resolve(ctx, step.Uses, policy)
	if err != nil {
		return StepResult{Error: err.Error()}, err
	}
	binding, err = s.Caps.EnsureAvailable(ctx, binding, policy)
	if err != nil {
		return StepResult{Error: err.Error()}, err
	}
	avail := domain.MapAvailability(binding.Status, domain.AllowsAutoStart(binding.Status, policy, true))
	if avail == domain.AvailBlocked || avail == domain.AvailError {
		reason := fmt.Sprintf("Required capability %q is provided by %s (%s). status=%s", binding.Capability, binding.ProviderID, binding.Kind, binding.Status)
		run.ParkBlocked(reason)
		if err := s.persist(ctx, run); err != nil {
			failure := errors.Join(fmt.Errorf("%s", reason), fmt.Errorf("persist blocked run state: %w", err))
			return StepResult{Error: failure.Error()}, failure
		}
		s.emit(contracts.EventAutomationRunBlocked, map[string]any{
			"run_id": run.ID, "capability": binding.Capability, "provider": binding.ProviderID, "status": binding.Status, "reason": binding.Reason,
		})
		return StepResult{Error: run.BlockedReason}, fmt.Errorf("%s", run.BlockedReason)
	}
	raw, err := json.Marshal(step.With)
	if err != nil {
		return StepResult{Error: fmt.Sprintf("encode capability arguments: %v", err)}, err
	}
	out, err := s.Caps.Execute(ctx, binding, raw)
	if err != nil {
		return StepResult{Error: err.Error()}, err
	}
	if strings.TrimSpace(string(out)) == "" {
		return StepResult{ExitCode: 0}, nil
	}
	var outputs map[string]any
	if err := json.Unmarshal([]byte(out), &outputs); err != nil {
		return StepResult{Error: fmt.Sprintf("decode capability output: %v", err)}, err
	}
	return StepResult{ExitCode: 0, Outputs: outputs}, nil
}

func (s *ExecutionScheduler) failJob(ctx context.Context, run *domain.WorkflowRun, jr *domain.JobRun, reason string) error {
	jr.Fail(reason, s.now())
	dag, _ := domain.BuildDAG(run.Definition.Jobs)
	cont := false
	if j := run.Definition.JobByID(jr.JobID); j != nil {
		cont = j.ContinueOnError
	}
	status := map[string]domain.RunStatus{}
	for i := range run.Jobs {
		status[run.Jobs[i].JobID] = run.Jobs[i].Status
	}
	for _, id := range domain.BlockedByFailure(dag, jr.JobID, cont, status) {
		if dep := run.JobRunByID(id); dep != nil {
			dep.ParkBlocked("upstream failed: " + jr.JobID)
		}
	}
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	s.emit(contracts.EventAutomationJobFailed, map[string]any{"run_id": run.ID, "job_id": jr.JobID, "error": reason})
	return nil
}

func (s *ExecutionScheduler) maybeFinalize(ctx context.Context, run *domain.WorkflowRun) error {
	sum := run.Summary()
	run.Finalize(s.now(), sum)
	if err := s.persist(ctx, run); err != nil {
		return err
	}
	if run.Status == domain.StatusFailed {
		s.emit(contracts.EventAutomationRunFailed, map[string]any{"run_id": run.ID})
	} else if run.Status == domain.StatusSuccess {
		s.emit(contracts.EventAutomationRunCompleted, map[string]any{"run_id": run.ID})
	}
	if run.Status.IsTerminal() {
		s.notifyWebhook(ctx, run)
	}
	return nil
}

// notifyWebhook fires the workflow webhook (if configured) in a
// fire-and-forget goroutine. Errors are logged but never block the run.
func (s *ExecutionScheduler) notifyWebhook(ctx context.Context, run *domain.WorkflowRun) {
	if s == nil || run == nil || s.Notifier == nil || strings.TrimSpace(run.Definition.WebhookURL) == "" {
		return
	}
	url := run.Definition.WebhookURL
	runID := run.ID
	s.goSafe("automation-webhook", func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				// A notifier or event emitter is an external boundary. Do not
				// let its panic escape the fire-and-forget goroutine; the event
				// is best effort and the run is already terminal.
				func() {
					defer func() { _ = recover() }()
					s.emit("automation.webhook.failed", map[string]any{
						"run_id": runID, "url": url, "error": fmt.Sprintf("webhook callback panicked: %v", recovered),
					})
				}()
			}
		}()
		if err := s.Notifier.NotifyRunCompleted(notifyCtx, url, run); err != nil {
			s.emit("automation.webhook.failed", map[string]any{"run_id": runID, "url": url, "error": err.Error()})
		}
	})
	_ = ctx
}

func (s *ExecutionScheduler) Cancel(ctx context.Context, runID string) error {
	if s == nil || s.Runs == nil {
		return fmt.Errorf("run store not configured")
	}
	run, err := s.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for id, cancel := range s.cancels {
		if s.cancelOwner[id] != runID {
			continue // belongs to another workflow run - leave it running
		}
		cancel()
		delete(s.cancels, id)
		delete(s.cancelOwner, id)
	}
	s.mu.Unlock()
	run.Cancel(s.now())
	if err := s.persist(ctx, run); err != nil {
		return fmt.Errorf("persist cancelled run %q: %w", runID, err)
	}
	s.emit(contracts.EventAutomationRunCancelled, map[string]any{"run_id": run.ID})
	return nil
}

func (s *ExecutionScheduler) RecoverStale(ctx context.Context) error {
	if s == nil || s.Runs == nil {
		return fmt.Errorf("run store not configured")
	}
	runs, err := s.Runs.List(ctx, RunFilter{})
	if err != nil {
		return err
	}
	now := s.now()
	var failures []error
	for _, run := range runs {
		if run == nil || run.Status != domain.StatusRunning {
			continue
		}
		changed := false
		for i := range run.Jobs {
			j := &run.Jobs[i]
			if j.Status == domain.StatusRunning && j.HeartbeatAt != nil && now.Sub(*j.HeartbeatAt) > time.Minute {
				j.Status = domain.StatusFailed
				j.Error = "stale lease after process restart"
				changed = true
			}
		}
		if changed {
			if err := s.persist(ctx, run); err != nil {
				failures = append(failures, fmt.Errorf("persist stale run %q: %w", run.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (s *ExecutionScheduler) persist(ctx context.Context, run *domain.WorkflowRun) error {
	if s == nil || s.Runs == nil {
		return fmt.Errorf("run store not configured")
	}
	if run == nil || strings.TrimSpace(run.ID) == "" {
		return fmt.Errorf("workflow run is empty")
	}
	runMu := s.lockRun(run.ID)
	runMu.Lock()
	defer runMu.Unlock()
	cur, err := s.Runs.Get(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("read run %q before update: %w", run.ID, err)
	}
	if cur == nil {
		return fmt.Errorf("read run %q before update: empty result", run.ID)
	}
	domain.MergeRun(cur, run)
	return s.Runs.Update(ctx, cur)
}

func (s *ExecutionScheduler) emit(typ string, v any) {
	if s == nil || s.Bus == nil {
		return
	}
	defer func() { _ = recover() }()
	s.Bus.Emit(typ, v)
}

func NewWorkflowRun(def domain.WorkflowDefinition, requestedBy string) *domain.WorkflowRun {
	run := &domain.WorkflowRun{
		TaskState: domain.TaskState[domain.RunStatus]{
			ID:     domain.NewID(domain.IDPrefixRun),
			Status: domain.StatusQueued,
		},
		WorkflowID:  def.ID,
		Name:        def.Name,
		Workspace:   def.Source.Workspace,
		Definition:  def,
		RequestedBy: requestedBy,
		CreatedAt:   clock.NewTime().Time(),
	}
	if run.WorkflowID == "" {
		run.WorkflowID = "pipeline"
	}
	for _, j := range def.Jobs {
		jr := domain.JobRun{
			TaskState: domain.TaskState[domain.RunStatus]{ID: domain.NewID(domain.IDPrefixJob), Status: domain.StatusQueued},
			JobID:     j.ID,
			Name:      j.Name,
		}
		for _, st := range j.Steps {
			jr.Steps = append(jr.Steps, domain.StepRun{
				TaskState: domain.TaskState[domain.RunStatus]{ID: domain.NewID(domain.IDPrefixStep), Status: domain.StatusQueued},
				StepID:    st.ID,
				Name:      st.Name,
			})
		}
		run.Jobs = append(run.Jobs, jr)
	}
	return run
}
