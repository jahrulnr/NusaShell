package domain

import "time"

// TriggerKind is the normalized internal trigger type.
type TriggerKind string

const (
	TriggerOnce     TriggerKind = "schedule.once"
	TriggerInterval TriggerKind = "schedule.interval"
	TriggerCron     TriggerKind = "schedule.cron"
	TriggerEvent    TriggerKind = "event"
	TriggerManual   TriggerKind = "manual"
)

// TriggerFamily is the user-facing once / every / when / manual grouping.
type TriggerFamily string

const (
	FamilyOnce   TriggerFamily = "once"
	FamilyEvery  TriggerFamily = "every"
	FamilyWhen   TriggerFamily = "when"
	FamilyManual TriggerFamily = "manual"
)

// Trigger is one way a workflow becomes eligible to start.
type Trigger struct {
	ID        string
	Kind      TriggerKind
	Family    TriggerFamily
	At        *time.Time
	Interval  time.Duration
	Cron      string
	Timezone  string
	Event     string
	Where     map[string]any
	Debounce  time.Duration
	Manual    bool
	AutoStart AutoStartPolicy
}

// Location loads the IANA timezone, defaulting to UTC.
func (t Trigger) Location() *time.Location {
	if t.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}
