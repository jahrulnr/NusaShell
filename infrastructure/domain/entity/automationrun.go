package entity

import (
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// AutomationRun is one firing of an automation: when it was due, when it
// started and ended, the thread its turn ran in, and how it ended.
type AutomationRun struct {
	id             string
	automationID   string
	scheduledAt    time.Time
	startedAt      time.Time
	endedAt        time.Time
	conversationID string
	status         enum.RunStatus
	// note says why a run failed, or what went wrong after it finished
	// (the reply could not reach its chat).
	note string
}

// NewAutomationRun starts a run of automationID.
func NewAutomationRun(automationID string, scheduledAt, startedAt time.Time) (*AutomationRun, error) {
	id, err := valueobject.NewID("run", "")
	if err != nil {
		return nil, err
	}
	return &AutomationRun{id: id.String(), automationID: automationID, scheduledAt: scheduledAt, startedAt: startedAt, status: enum.RunRunning}, nil
}

// RestoreAutomationRun hydrates a stored run.
func RestoreAutomationRun(id, automationID string, scheduledAt, startedAt, endedAt time.Time, conversationID string, status enum.RunStatus, note string) *AutomationRun {
	return &AutomationRun{id: id, automationID: automationID, scheduledAt: scheduledAt, startedAt: startedAt, endedAt: endedAt, conversationID: conversationID, status: status, note: note}
}

// Bind records the thread the run's turn runs in.
func (r *AutomationRun) Bind(convID string) { r.conversationID = convID }

// Finish ends the run. A run already ended keeps its outcome.
func (r *AutomationRun) Finish(status enum.RunStatus, note string, at time.Time) {
	if r.status.Terminal() || !status.Terminal() {
		return
	}
	r.status, r.note, r.endedAt = status, strings.TrimSpace(note), at
}

// AddNote appends a later problem, such as a reply that could not reach
// its chat, without changing the outcome.
func (r *AutomationRun) AddNote(note string) {
	note = strings.TrimSpace(note)
	if note == "" {
		return
	}
	if r.note != "" {
		r.note += "; "
	}
	r.note += note
}

func (r AutomationRun) ID() string             { return r.id }
func (r AutomationRun) AutomationID() string   { return r.automationID }
func (r AutomationRun) ScheduledAt() time.Time { return r.scheduledAt }
func (r AutomationRun) StartedAt() time.Time   { return r.startedAt }
func (r AutomationRun) EndedAt() time.Time     { return r.endedAt }
func (r AutomationRun) ConversationID() string { return r.conversationID }
func (r AutomationRun) Status() enum.RunStatus { return r.status }
func (r AutomationRun) Note() string           { return r.note }
