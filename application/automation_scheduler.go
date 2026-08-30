package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
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
	Bus       *Bus
}

func (s *AutomationScheduler) now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

// EnableWorkflow persists schedules/subscriptions for an enabled workflow.
func (s *AutomationScheduler) EnableWorkflow(ctx context.Context, w *domain.WorkflowDefinition) error {
	if reason := s.invalidReason(ctx, w); reason != "" {
		return fmt.Errorf("invalid workflow: %s", reason)
	}
	w.Enabled = true
	if err := s.Workflows.Put(ctx, w); err != nil {
		return err
	}
	now := s.now()
	for i, t := range w.Triggers {
		if t.Kind == domain.TriggerEvent {
			if s.Caps != nil && t.Event != "" {
				binding, err := s.Caps.Resolve(ctx, t.Event, t.AutoStart)
				if err == nil || binding.Status != domain.CapMissing {
					binding, _ = s.Caps.EnsureAvailable(ctx, binding, t.AutoStart)
					avail := domain.MapAvailability(binding.Status, domain.AllowsAutoStart(binding.Status, t.AutoStart, true))
					if avail == domain.AvailBlocked {
						return s.blockWorkflow(ctx, w, binding)
					}
				}
			}
			continue
		}
		if t.Kind == domain.TriggerManual {
			continue
		}
		next, err := domain.NextFire(t, now, nil, w.Missed)
		if err != nil {
			return err
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
			Status: domain.SchedulePending, CreatedAt: now.UTC(),
		}
		if err := s.Schedules.Put(ctx, rec); err != nil {
			return err
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
	_ = s.Workflows.Put(ctx, w)
	if s.Bus != nil {
		s.Bus.Emit(contracts.EventCIRunBlocked, map[string]any{
			"workflow_id": w.ID, "capability": b.Capability, "provider": b.ProviderID, "status": b.Status, "reason": b.Reason,
		})
	}
	return nil
}

// FireDue claims due schedules and starts runs.
func (s *AutomationScheduler) FireDue(ctx context.Context) error {
	if s.Schedules == nil {
		return nil
	}
	now := s.now()
	due, err := s.Schedules.Due(ctx, now, 32)
	if err != nil {
		return err
	}
	for _, rec := range due {
		claimed, err := s.Schedules.Claim(ctx, rec.ID, now)
		if err != nil || claimed == nil {
			continue
		}
		w, err := s.Workflows.Get(ctx, rec.WorkflowID)
		if err != nil || !w.Enabled {
			continue
		}
		if err := s.startFromTrigger(ctx, w, rec.TriggerID, "", nil); err != nil {
			return err
		}
		var trig domain.Trigger
		for _, t := range w.Triggers {
			if t.ID == rec.TriggerID || rec.TriggerID == t.ID {
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
		if err != nil || next == nil {
			continue
		}
		rec.Status = domain.SchedulePending
		rec.FiredAt = nil
		rec.NextRunAt = *next
		_ = s.Schedules.Put(ctx, rec)
	}
	return s.resumeWaits(ctx)
}

func (s *AutomationScheduler) resumeWaits(ctx context.Context) error {
	if s.Waits == nil || s.Exec == nil {
		return nil
	}
	due, err := s.Waits.Due(ctx, s.now(), 32)
	if err != nil {
		return err
	}
	for _, w := range due {
		claimed, err := s.Waits.Claim(ctx, w.ID)
		if err != nil || claimed == nil {
			continue
		}
		_ = s.Exec.Tick(ctx, claimed.WorkflowRunID)
	}
	return nil
}

// IngestEvent matches when-triggers and creates at-most-one run per delivery key.
func (s *AutomationScheduler) IngestEvent(ctx context.Context, ev domain.Event) error {
	if ev.ID == "" {
		ev.ID = domain.NewID("evt")
	}
	if ev.Time.IsZero() {
		ev.Time = s.now().UTC()
	}
	if s.Events != nil {
		_ = s.Events.PutEvent(ctx, &ev)
	}
	if s.Bus != nil {
		s.Bus.Emit(contracts.EventAutomationEvent, ev)
	}
	if s.Workflows == nil {
		return nil
	}
	list, err := s.Workflows.List(ctx)
	if err != nil {
		return err
	}
	for _, w := range list {
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
				last, ok, _ := s.Debounce.Last(ctx, w.ID, t.ID)
				if ok && s.now().Sub(last) < t.Debounce {
					continue
				}
			}
			if s.Caps != nil {
				binding, _ := s.Caps.Resolve(ctx, t.Event, t.AutoStart)
				// RPC ingest already produced the event; a missing source
				// provider must not drop it. Disabled providers still block.
				if binding.Status == domain.CapDisabled {
					_ = s.blockWorkflow(ctx, w, binding)
					continue
				}
			}
			created := true
			if s.Events != nil {
				var err error
				created, err = s.Events.RecordDelivery(ctx, ev.ID, t.ID, w.ID, "", s.now())
				if err != nil {
					return err
				}
			}
			if !created {
				continue
			}
			if err := s.startFromTrigger(ctx, w, t.ID, ev.ID, &ev); err != nil {
				return err
			}
			if s.Debounce != nil {
				_ = s.Debounce.Touch(ctx, w.ID, t.ID, s.now())
			}
		}
	}
	if s.Waits != nil {
		waiting, _ := s.Waits.WaitingForEvent(ctx, ev.Type)
		for _, rec := range waiting {
			if domain.MatchWhere(ev, rec.Where) {
				claimed, _ := s.Waits.Claim(ctx, rec.ID)
				if claimed != nil && s.Exec != nil {
					_ = s.Exec.Tick(ctx, claimed.WorkflowRunID)
				}
			}
		}
	}
	return nil
}

func (s *AutomationScheduler) startFromTrigger(ctx context.Context, w *domain.WorkflowDefinition, triggerID, eventID string, ev *domain.Event) error {
	if s.Caps != nil {
		for _, j := range w.Jobs {
			for _, step := range j.Steps {
				name := strings.TrimSpace(step.Uses)
				if name == "" {
					continue
				}
				b, err := s.Caps.Resolve(ctx, name, domain.DefaultAutoStart)
				if err != nil && b.Status == domain.CapMissing {
					return nil
				}
				if b.Kind == domain.CapabilityMCP {
					b, _ = s.Caps.EnsureAvailable(ctx, b, domain.DefaultAutoStart)
				}
				avail := domain.MapAvailability(b.Status, domain.AllowsAutoStart(b.Status, domain.DefaultAutoStart, true))
				if avail == domain.AvailBlocked {
					_ = s.blockWorkflow(ctx, w, b)
					return nil
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
		active, ok, _ := s.Locks.Active(ctx, key)
		if ok {
			switch policy {
			case domain.ConcurrencySkip:
				return nil
			case domain.ConcurrencyReplace:
				if s.Exec != nil {
					_ = s.Exec.Cancel(ctx, active)
				}
				_ = s.Locks.Release(ctx, key, active)
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
		run.RequestedBy = "event"
	}
	if s.Locks != nil && policy != domain.ConcurrencyAllow {
		_ = s.Locks.Acquire(ctx, key, run.ID)
	}
	if s.Exec == nil {
		return fmt.Errorf("execution scheduler not configured")
	}
	err := s.Exec.StartRun(ctx, run)
	if s.Locks != nil && policy != domain.ConcurrencyAllow {
		_ = s.Locks.Release(ctx, key, run.ID)
	}
	return err
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
