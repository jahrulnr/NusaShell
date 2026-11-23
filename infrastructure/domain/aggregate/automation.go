package aggregate

import (
	"errors"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ErrAutomationChanged rejects a write based on an out-of-date read of an
// automation: it was saved or fired in between.
var ErrAutomationChanged = errors.New("automation was changed elsewhere")

// MaxAutomationPrompt bounds the prompt an automation sends each run.
const MaxAutomationPrompt = 100_000

// Automation sends a prompt to a runtime on a schedule. Each firing is one
// run: a turn in a thread, either a new thread per run or one thread the
// automation keeps. A run's final reply can also go to a chat.
type Automation struct {
	id      string
	name    string
	prompt  string
	agentID string
	// providerID/modelID name the model for a provider run; for an agent
	// run modelID is the agent's model, empty for the agent's default.
	providerID string
	modelID    string
	schedule   valueobject.Schedule
	thread     enum.AutomationThread
	// conversationID is the thread a "same" automation keeps; empty until
	// its first run opens one.
	conversationID string
	projectID      string
	notify         *valueobject.ChatRef
	expiresAt      time.Time
	enabled        bool
	// nextRunAt is the next firing; zero when nothing is left to fire.
	nextRunAt time.Time
	createdAt time.Time
	updatedAt time.Time
	// version is the write concurrency token: the repository bumps it on
	// every stored change and refuses a write based on an older one.
	version int64
}

// NewAutomation builds an enabled automation. An empty id mints one.
// Runtime and schedule are set before it is saved.
func NewAutomation(id, name, prompt string, now time.Time) (*Automation, error) {
	vo, err := valueobject.NewID("au", id)
	if err != nil {
		return nil, err
	}
	a := &Automation{id: vo.String(), thread: enum.AutomationThreadNew, enabled: true, createdAt: now, updatedAt: now}
	if err := a.Describe(name, prompt); err != nil {
		return nil, err
	}
	return a, nil
}

// Describe sets the name and the prompt every run sends.
func (a *Automation) Describe(name, prompt string) error {
	name, prompt = strings.TrimSpace(name), strings.TrimSpace(prompt)
	if name == "" {
		return errors.New("an automation needs a name")
	}
	if prompt == "" {
		return errors.New("an automation needs a prompt")
	}
	if len(prompt) > MaxAutomationPrompt {
		return errors.New("the prompt is too long")
	}
	a.name, a.prompt = name, prompt
	return nil
}

// SetRuntime picks what runs: an agent (with an optional model), or a
// provider and one of its models. Changing it starts a new kept thread,
// since a thread's history belongs to how it was run.
func (a *Automation) SetRuntime(agentID, providerID, modelID string) error {
	agentID, providerID, modelID = strings.TrimSpace(agentID), strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	switch {
	case agentID != "" && providerID != "":
		return errors.New("an automation runs on an agent or a provider, not both")
	case agentID == "" && providerID == "":
		return errors.New("an automation needs an agent or a provider and model")
	case providerID != "" && modelID == "":
		return errors.New("a provider run needs a model")
	}
	if agentID != a.agentID || providerID != a.providerID || modelID != a.modelID {
		a.conversationID = ""
	}
	a.agentID, a.providerID, a.modelID = agentID, providerID, modelID
	return nil
}

// SetSchedule replaces when the automation fires and plans its next run
// from now; an unchanged schedule keeps its plan. A new one-time moment
// more than a minute in the past is refused:
// it would fire at once, which is what Run now is for.
func (a *Automation) SetSchedule(s valueobject.Schedule, now time.Time) error {
	if s.IsZero() {
		return errors.New("an automation needs a schedule")
	}
	if a.schedule.Same(s) {
		return nil // an unchanged schedule keeps its plan, even a spent one
	}
	if s.Kind() == valueobject.ScheduleOnce && s.At().Before(now.Add(-time.Minute)) {
		return errors.New("that time has already passed")
	}
	a.schedule = s
	a.plan(now)
	return nil
}

func (a *Automation) plan(now time.Time) {
	if a.schedule.Kind() == valueobject.ScheduleOnce {
		a.nextRunAt = a.schedule.At()
		return
	}
	a.nextRunAt = a.schedule.Next(now)
}

// SetThread picks a new thread per run or one kept thread.
func (a *Automation) SetThread(t enum.AutomationThread) {
	if t != a.thread {
		a.conversationID = ""
	}
	a.thread = t
}

// SetProject runs new threads inside a project; "" runs them outside any.
func (a *Automation) SetProject(projectID string) { a.projectID = strings.TrimSpace(projectID) }

// SetNotify sends each run's final reply to chat too; nil keeps results
// in the thread only.
func (a *Automation) SetNotify(chat *valueobject.ChatRef) { a.notify = chat }

// SetExpiry stops the automation from firing after at; zero never expires.
// A new non-zero expiry already passed is refused; an unchanged one is
// kept, so an expired automation can still be edited. When an automation expired
// at now is un-expired — a later expiry or none — its next firing is
// re-planned from now, so a stale nextRunAt cannot fire a catch-up run.
func (a *Automation) SetExpiry(at, now time.Time) error {
	if !at.IsZero() && !at.Equal(a.expiresAt) && !at.After(now) {
		return errors.New("the expiry has already passed")
	}
	wasExpired := a.Expired(now)
	a.expiresAt = at
	if wasExpired && !a.schedule.IsZero() {
		a.plan(now)
	}
	return nil
}

// SetEnabled pauses or resumes. Resuming a repeating schedule plans from
// now, so runs missed while paused do not fire; a one-time run missed
// while paused fires on resume.
func (a *Automation) SetEnabled(enabled bool, now time.Time) {
	if enabled && !a.enabled && a.schedule.Kind() == valueobject.ScheduleCron {
		a.plan(now)
	}
	a.enabled = enabled
}

// Expired reports whether the automation is past its expiry at now.
func (a Automation) Expired(now time.Time) bool {
	return !a.expiresAt.IsZero() && now.After(a.expiresAt)
}

// Due reports whether a run should start at now.
func (a Automation) Due(now time.Time) bool {
	return a.enabled && !a.nextRunAt.IsZero() && !a.nextRunAt.After(now) && !a.Expired(now)
}

// Claim takes the due firing and plans the one after it. A repeating
// schedule that missed several firings runs once and moves past them.
// It returns the moment the run was scheduled for. Claiming is not an
// owner edit: updatedAt stays.
func (a *Automation) Claim(now time.Time) time.Time {
	scheduled := a.nextRunAt
	a.nextRunAt = a.schedule.Next(now)
	return scheduled
}

// KeepConversation records the thread a "same" automation continues.
func (a *Automation) KeepConversation(convID string) {
	if a.thread == enum.AutomationThreadSame {
		a.conversationID = convID
	}
}

// Touch records an owner edit at now. A save based on a read whose
// updatedAt is not the stored one is refused with ErrAutomationChanged by
// the repository.
func (a *Automation) Touch(now time.Time) { a.updatedAt = now }

// BumpVersion marks the write that just persisted this automation; the
// repository calls it after a successful guarded write so the next one is
// based on the version now stored.
func (a *Automation) BumpVersion() { a.version++ }

// Restore hydrates stored state without re-planning or re-validating it.
func (a *Automation) Restore(s valueobject.Schedule, enabled bool, nextRunAt time.Time, conversationID string,
	expiresAt, createdAt, updatedAt time.Time, version int64) {
	a.schedule, a.enabled, a.nextRunAt, a.conversationID = s, enabled, nextRunAt, conversationID
	a.expiresAt, a.createdAt, a.updatedAt, a.version = expiresAt, createdAt, updatedAt, version
}

func (a Automation) ID() string                     { return a.id }
func (a Automation) Name() string                   { return a.name }
func (a Automation) Prompt() string                 { return a.prompt }
func (a Automation) AgentID() string                { return a.agentID }
func (a Automation) ProviderID() string             { return a.providerID }
func (a Automation) ModelID() string                { return a.modelID }
func (a Automation) Schedule() valueobject.Schedule { return a.schedule }
func (a Automation) Thread() enum.AutomationThread  { return a.thread }
func (a Automation) ConversationID() string         { return a.conversationID }
func (a Automation) ProjectID() string              { return a.projectID }
func (a Automation) Notify() *valueobject.ChatRef   { return a.notify }
func (a Automation) ExpiresAt() time.Time           { return a.expiresAt }
func (a Automation) Enabled() bool                  { return a.enabled }
func (a Automation) NextRunAt() time.Time           { return a.nextRunAt }
func (a Automation) CreatedAt() time.Time           { return a.createdAt }
func (a Automation) UpdatedAt() time.Time           { return a.updatedAt }
func (a Automation) Version() int64                 { return a.version }
