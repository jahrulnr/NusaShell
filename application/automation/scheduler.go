package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// AutomationScheduler wakes time-based triggers, consumes events, and
// creates workflow runs. It does not execute jobs.
type AutomationScheduler struct {
	Workflows WorkflowStore
	Schedules ScheduleStore
	Events    EventStore
	Waits     WaitStore
	Locks     RunLockStore
	Debounce  DebounceStore
	Caps      CapabilityResolver
	Exec      *ExecutionScheduler
	Clock     Clock
	Bus       Emitter
}

func (s *AutomationScheduler) now() time.Time {
	if s.Clock == nil {
		return clock.NewTime().Time()
	}
	return clock.NewTime(s.Clock.Now()).Time()
}

func (s *AutomationScheduler) emit(typ string, value any) {
	if s == nil || s.Bus == nil {
		return
	}
	defer func() { _ = recover() }()
	s.Bus.Emit(typ, value)
}

// EnableWorkflow persists schedules/subscriptions for an enabled workflow.
func (s *AutomationScheduler) EnableWorkflow(ctx context.Context, w *domain.WorkflowDefinition) error {
	if s == nil {
		return fmt.Errorf("automation scheduler not configured")
	}
	if w == nil {
		return fmt.Errorf("workflow is empty")
	}
	if s.Workflows == nil {
		return fmt.Errorf("workflow store not configured")
	}
	if reason := s.invalidReason(ctx, w); reason != "" {
		return fmt.Errorf("invalid workflow: %s", reason)
	}
	w.Enabled = true
	if err := s.Workflows.Put(ctx, w); err != nil {
		w.Enabled = false
		return err
	}
	activationFailure := func(err error) error {
		w.Enabled = false
		failures := []error{err}
		if s.Schedules != nil {
			if cleanupErr := s.cancelWorkflowSchedules(ctx, w.ID); cleanupErr != nil {
				failures = append(failures, fmt.Errorf("cancel partial schedules: %w", cleanupErr))
			}
		}
		if rollbackErr := s.Workflows.Put(ctx, w); rollbackErr != nil {
			failures = append(failures, fmt.Errorf("disable rollback: %w", rollbackErr))
		}
		return errors.Join(failures...)
	}
	now := s.now()
	if s.Schedules != nil {
		if err := s.cancelWorkflowSchedules(ctx, w.ID); err != nil {
			return activationFailure(fmt.Errorf("reset schedules for workflow %q: %w", w.ID, err))
		}
	}
	for i, t := range w.Triggers {
		if t.Kind == domain.TriggerEvent {
			if s.Caps != nil && t.Event != "" {
				binding, err := s.Caps.Resolve(ctx, t.Event, t.AutoStart)
				if err != nil {
					// A generic event publisher may not be represented by a
					// capability. Only that expected absence is optional; a
					// registry/storage failure must not activate blindly.
					if binding.Status == domain.CapMissing {
						continue
					}
					return activationFailure(fmt.Errorf("resolve event capability %q: %w", t.Event, err))
				}
				binding, err = s.Caps.EnsureAvailable(ctx, binding, t.AutoStart)
				if err != nil {
					return activationFailure(fmt.Errorf("ensure event capability %q: %w", t.Event, err))
				}
				avail := domain.MapAvailability(binding.Status, domain.AllowsAutoStart(binding.Status, t.AutoStart, true))
				switch avail {
				case domain.AvailBlocked:
					if err := s.blockWorkflow(ctx, w, binding); err != nil {
						return activationFailure(err)
					}
				case domain.AvailError:
					return activationFailure(fmt.Errorf("event capability %q is unavailable: %s", t.Event, binding.Reason))
				}
			}
			continue
		}
		if t.Kind == domain.TriggerManual {
			continue
		}
		if s.Schedules == nil {
			return activationFailure(fmt.Errorf("schedule store not configured"))
		}
		next, err := domain.NextFire(t, now, nil, w.Missed)
		if err != nil {
			return activationFailure(err)
		}
		if next == nil {
			continue
		}
		id := t.ID
		if id == "" {
			id = fmt.Sprintf("%s:t%d", w.ID, i)
		}
		rec := &domain.ScheduleRecord{
			ID: id, WorkflowID: w.ID, TriggerID: id, Kind: t.Kind,
			RunAt: *next, NextRunAt: *next, Timezone: t.Timezone,
			Status: domain.SchedulePending, CreatedAt: now,
		}
		if err := s.Schedules.Put(ctx, rec); err != nil {
			return activationFailure(fmt.Errorf("register schedule %q: %w", id, err))
		}
	}
	return nil
}

// DisableWorkflow persists a disabled workflow and retires its pending
// schedules so a later re-enable cannot replay an obsolete definition.
func (s *AutomationScheduler) DisableWorkflow(ctx context.Context, w *domain.WorkflowDefinition) error {
	if s == nil {
		return fmt.Errorf("automation scheduler not configured")
	}
	if w == nil {
		return fmt.Errorf("workflow is empty")
	}
	if s.Workflows == nil {
		return fmt.Errorf("workflow store not configured")
	}
	w.Enabled = false
	if err := s.Workflows.Put(ctx, w); err != nil {
		return err
	}
	if s.Schedules != nil {
		if err := s.cancelWorkflowSchedules(ctx, w.ID); err != nil {
			return fmt.Errorf("reset schedules for disabled workflow %q: %w", w.ID, err)
		}
	}
	return nil
}

func (s *AutomationScheduler) cancelWorkflowSchedules(ctx context.Context, workflowID string) error {
	records, err := s.Schedules.List(ctx)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if rec == nil || rec.WorkflowID != workflowID || rec.Status != domain.SchedulePending {
			continue
		}
		rec.Status = domain.ScheduleCancelled
		if err := s.Schedules.Put(ctx, rec); err != nil {
			return fmt.Errorf("cancel schedule %q: %w", rec.ID, err)
		}
	}
	return nil
}

func (s *AutomationScheduler) invalidReason(ctx context.Context, w *domain.WorkflowDefinition) string {
	if w == nil {
		return "workflow is empty"
	}
	if msg := strings.TrimSpace(w.Source.ParseError); msg != "" {
		return msg
	}
	r := s.Validate(ctx, w)
	if r.Verdict() != "INVALID" {
		return ""
	}
	return firstValidationMessage(r)
}

func (s *AutomationScheduler) blockWorkflow(ctx context.Context, w *domain.WorkflowDefinition, b domain.CapabilityBinding) error {
	if s == nil || s.Workflows == nil {
		return fmt.Errorf("workflow store not configured")
	}
	if err := s.Workflows.Put(ctx, w); err != nil {
		return fmt.Errorf("persist blocked workflow %q: %w", w.ID, err)
	}
	if s.Bus != nil {
		s.emit(contracts.EventAutomationRunBlocked, map[string]any{
			"workflow_id": w.ID, "capability": b.Capability, "provider": b.ProviderID, "status": b.Status, "reason": b.Reason,
		})
	}
	return nil
}

// FireDue claims due schedules and starts runs.
func (s *AutomationScheduler) FireDue(ctx context.Context) error {
	if s == nil || s.Schedules == nil {
		return nil
	}
	if s.Workflows == nil {
		return fmt.Errorf("workflow store not configured")
	}
	now := s.now()
	due, err := s.Schedules.Due(ctx, now, 32)
	if err != nil {
		return err
	}
	for _, rec := range due {
		if rec == nil {
			continue
		}
		claimed, err := s.Schedules.Claim(ctx, rec.ID, now)
		if err != nil {
			return fmt.Errorf("claim schedule %q: %w", rec.ID, err)
		}
		if claimed == nil {
			continue
		}
		w, err := s.Workflows.Get(ctx, rec.WorkflowID)
		if err != nil {
			return fmt.Errorf("load workflow %q for schedule %q: %w", rec.WorkflowID, rec.ID, err)
		}
		if w == nil || !w.Enabled {
			continue
		}
		if err := s.startFromTrigger(ctx, w, rec.TriggerID, "", nil); err != nil {
			return err
		}
		var trig domain.Trigger
		for _, t := range w.Triggers {
			if t.ID == rec.TriggerID {
				trig = t
				break
			}
		}
		if trig.Kind == "" {
			trig.Kind = rec.Kind
		}
		if trig.Kind == domain.TriggerOnce {
			continue
		}
		last := rec.NextRunAt
		next, err := domain.NextFire(trig, now, &last, w.Missed)
		if err != nil {
			return fmt.Errorf("reschedule %q: %w", rec.ID, err)
		}
		if next == nil {
			continue
		}
		rec.Status = domain.SchedulePending
		rec.FiredAt = nil
		rec.NextRunAt = *next
		if err := s.Schedules.Put(ctx, rec); err != nil {
			return fmt.Errorf("reschedule %q: %w", rec.ID, err)
		}
	}
	return s.resumeWaits(ctx)
}

func (s *AutomationScheduler) resumeWaits(ctx context.Context) error {
	if s == nil || s.Waits == nil || s.Exec == nil {
		return nil
	}
	due, err := s.Waits.Due(ctx, s.now(), 32)
	if err != nil {
		return err
	}
	for _, w := range due {
		if w == nil {
			continue
		}
		claimed, err := s.Waits.Claim(ctx, w.ID)
		if err != nil {
			return fmt.Errorf("claim wait %q: %w", w.ID, err)
		}
		if claimed == nil {
			continue
		}
		if err := s.Exec.Tick(ctx, claimed.WorkflowRunID); err != nil {
			return fmt.Errorf("resume run %q: %w", claimed.WorkflowRunID, err)
		}
	}
	return nil
}

// IngestEvent matches when-triggers and creates at-most-one run per delivery key.
func (s *AutomationScheduler) IngestEvent(ctx context.Context, ev domain.Event) error {
	if s == nil {
		return fmt.Errorf("automation scheduler not configured")
	}
	ev.Type = strings.TrimSpace(ev.Type)
	if ev.Type == "" {
		return fmt.Errorf("event type is required")
	}
	if s.Events == nil {
		return fmt.Errorf("event store not configured")
	}
	if ev.ID == "" {
		ev.ID = domain.NewID(domain.IDPrefixEvt)
	}
	if ev.Time.IsZero() {
		ev.Time = s.now()
	}
	if err := s.Events.PutEvent(ctx, &ev); err != nil {
		return fmt.Errorf("persist event: %w", err)
	}
	if s.Bus != nil {
		s.emit(contracts.EventAutomationEvent, ev)
	}
	if s.Workflows == nil {
		return nil
	}
	list, err := s.Workflows.List(ctx)
	if err != nil {
		return err
	}
	for _, w := range list {
		if w == nil {
			return fmt.Errorf("workflow store returned an empty workflow")
		}
		if !w.Enabled {
			continue
		}
		for _, t := range w.Triggers {
			if t.Kind != domain.TriggerEvent {
				continue
			}
			if !ev.Match(t.Event, t.Where) {
				continue
			}
			if t.Debounce > 0 && s.Debounce != nil {
				last, ok, err := s.Debounce.Last(ctx, w.ID, t.ID)
				if err != nil {
					return fmt.Errorf("read debounce for workflow %q trigger %q: %w", w.ID, t.ID, err)
				}
				if ok && s.now().Sub(last) < t.Debounce {
					continue
				}
			}
			if s.Caps != nil {
				binding, resolveErr := s.Caps.Resolve(ctx, t.Event, t.AutoStart)
				// RPC ingest already produced the event; a missing source
				// provider must not drop it. Disabled providers still block.
				if resolveErr != nil && binding.Status != domain.CapMissing {
					return fmt.Errorf("resolve event capability %q: %w", t.Event, resolveErr)
				}
				if binding.Status == domain.CapDisabled {
					if err := s.blockWorkflow(ctx, w, binding); err != nil {
						return err
					}
					continue
				}
			}
			created, err := s.Events.RecordDelivery(ctx, ev.ID, t.ID, w.ID, "", s.now())
			if err != nil {
				return fmt.Errorf("record delivery for workflow %q trigger %q: %w", w.ID, t.ID, err)
			}
			if !created {
				continue
			}
			if err := s.startFromTrigger(ctx, w, t.ID, ev.ID, &ev); err != nil {
				var startFailure *runStartFailure
				if errors.As(err, &startFailure) && !startFailure.created {
					if rollback, ok := s.Events.(DeliveryRollback); ok {
						if rollbackErr := rollback.DeleteDelivery(ctx, ev.ID, t.ID, w.ID); rollbackErr != nil {
							return errors.Join(err, fmt.Errorf("rollback delivery for workflow %q trigger %q: %w", w.ID, t.ID, rollbackErr))
						}
					}
				}
				return err
			}
			if s.Debounce != nil {
				if err := s.Debounce.Touch(ctx, w.ID, t.ID, s.now()); err != nil {
					return fmt.Errorf("write debounce for workflow %q trigger %q: %w", w.ID, t.ID, err)
				}
			}
		}
	}
	if s.Waits != nil {
		waiting, err := s.Waits.WaitingForEvent(ctx, ev.Type)
		if err != nil {
			return fmt.Errorf("find waits for event %q: %w", ev.Type, err)
		}
		for _, rec := range waiting {
			if rec == nil || !domain.MatchWhere(ev, rec.Where) {
				continue
			}
			claimed, err := s.Waits.Claim(ctx, rec.ID)
			if err != nil {
				return fmt.Errorf("claim event wait %q: %w", rec.ID, err)
			}
			if claimed != nil && s.Exec != nil {
				if err := s.Exec.Tick(ctx, claimed.WorkflowRunID); err != nil {
					return fmt.Errorf("resume event wait run %q: %w", claimed.WorkflowRunID, err)
				}
			}
		}
	}
	return nil
}

func (s *AutomationScheduler) startFromTrigger(ctx context.Context, w *domain.WorkflowDefinition, triggerID, eventID string, ev *domain.Event) error {
	if s == nil {
		return &runStartFailure{err: fmt.Errorf("automation scheduler not configured")}
	}
	if w == nil {
		return &runStartFailure{err: fmt.Errorf("workflow is empty")}
	}
	if s.Exec == nil {
		return &runStartFailure{err: fmt.Errorf("execution scheduler not configured")}
	}
	if s.Caps != nil {
		for _, j := range w.Jobs {
			for _, step := range j.Steps {
				name := strings.TrimSpace(step.Uses)
				if name == "" {
					continue
				}
				b, err := s.Caps.Resolve(ctx, name, domain.DefaultAutoStart)
				if err != nil {
					return &runStartFailure{err: fmt.Errorf("resolve capability %q: %w", name, err)}
				}
				if b.Kind == domain.CapabilityMCP {
					b, err = s.Caps.EnsureAvailable(ctx, b, domain.DefaultAutoStart)
					if err != nil {
						return &runStartFailure{err: fmt.Errorf("ensure capability %q: %w", name, err)}
					}
				}
				avail := domain.MapAvailability(b.Status, domain.AllowsAutoStart(b.Status, domain.DefaultAutoStart, true))
				switch avail {
				case domain.AvailBlocked:
					if err := s.blockWorkflow(ctx, w, b); err != nil {
						return &runStartFailure{err: err}
					}
					return nil
				case domain.AvailError:
					return &runStartFailure{err: fmt.Errorf("capability %q is unavailable: %s", name, b.Reason)}
				}
			}
		}
	}
	key := w.Concurrency.Normalized().Key
	if key == "" {
		key = w.ID
	}
	policy := w.Concurrency.Normalized().Policy
	if s.Locks != nil && policy != domain.ConcurrencyAllow {
		active, ok, err := s.Locks.Active(ctx, key)
		if err != nil {
			return &runStartFailure{err: fmt.Errorf("inspect concurrency lock %q: %w", key, err)}
		}
		if ok {
			switch policy {
			case domain.ConcurrencySkip:
				return nil
			case domain.ConcurrencyReplace:
				if err := s.Exec.Cancel(ctx, active); err != nil {
					return &runStartFailure{err: fmt.Errorf("cancel active run %q: %w", active, err)}
				}
				if err := s.Locks.Release(ctx, key, active); err != nil {
					return &runStartFailure{err: fmt.Errorf("release concurrency lock %q: %w", key, err)}
				}
			case domain.ConcurrencyQueue:
				// leave the previous lock; skip starting a second run
				return nil
			}
		}
	}
	run := NewWorkflowRun(*w, "schedule")
	run.TriggerID = triggerID
	run.EventID = eventID
	if ev != nil {
		run.Event = ev
		run.RequestedBy = "event"
	}
	lockHeld := false
	if s.Locks != nil && policy != domain.ConcurrencyAllow {
		if err := s.Locks.Acquire(ctx, key, run.ID); err != nil {
			return &runStartFailure{err: fmt.Errorf("acquire concurrency lock %q: %w", key, err)}
		}
		lockHeld = true
	}
	var startErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				startErr = &runStartFailure{created: true, err: fmt.Errorf("start workflow run panicked: %v", recovered)}
			}
		}()
		startErr = s.Exec.StartRun(ctx, run)
	}()
	if !lockHeld {
		return startErr
	}
	releaseErr := s.Locks.Release(ctx, key, run.ID)
	if startErr != nil && releaseErr != nil {
		return errors.Join(startErr, fmt.Errorf("release concurrency lock %q: %w", key, releaseErr))
	}
	if startErr != nil {
		return startErr
	}
	if releaseErr != nil {
		return fmt.Errorf("release concurrency lock %q: %w", key, releaseErr)
	}
	return nil
}

func (s *AutomationScheduler) Validate(ctx context.Context, w *domain.WorkflowDefinition) domain.ValidationResult {
	r := domain.ValidateSyntax(w)
	if r.Verdict() == "INVALID" {
		return r
	}
	if s.Caps == nil {
		return r
	}
	for _, name := range w.ReferencedCapabilities() {
		b, err := s.Caps.Resolve(ctx, name, domain.DefaultAutoStart)
		if err != nil || b.Status == domain.CapMissing {
			r.Add(domain.ValidationIssue{
				Path: "capabilities", Code: "unknown_capability",
				Message: fmt.Sprintf("capability %q does not exist", name),
				Level:   domain.ValidationCapabilities,
			})
			continue
		}
		avail := domain.MapAvailability(b.Status, domain.AllowsAutoStart(b.Status, domain.DefaultAutoStart, true))
		if avail == domain.AvailBlocked {
			r.ProviderID = b.ProviderID
			r.Add(domain.ValidationIssue{
				Path: "providers", Code: "provider_" + string(b.Status),
				Message: fmt.Sprintf("provider %s is %s", b.ProviderID, b.Status),
				Level:   domain.ValidationProviders,
			})
		}
	}
	return r
}
