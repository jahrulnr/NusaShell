package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduleRecord is the durable timer row for once/every triggers.
type ScheduleRecord struct {
	ID         string
	WorkflowID string
	TriggerID  string
	Kind       TriggerKind
	RunAt      time.Time
	NextRunAt  time.Time
	Timezone   string
	Status     ScheduleStatus
	CreatedAt  time.Time
	FiredAt    *time.Time
}

// ScheduleStatus is the lifecycle of a persisted timer.
type ScheduleStatus string

const (
	SchedulePending   ScheduleStatus = "pending"
	ScheduleFired     ScheduleStatus = "fired"
	ScheduleCancelled ScheduleStatus = "cancelled"
	ScheduleExpired   ScheduleStatus = "expired"
)

// WaitRecord is a durable wake-up for wait_until / event wait. A waiting
// workflow must never keep a runner occupied.
type WaitRecord struct {
	ID            string
	WorkflowRunID string
	JobID         string
	StepID        string
	WakeAt        *time.Time
	EventType     string
	Where         map[string]any
	Timezone      string
	Status        ScheduleStatus
}

// NextFire returns the next fire time after now for a time-based trigger.
// Event and manual triggers return nil.
func NextFire(t Trigger, now time.Time, last *time.Time, missed MissedRunPolicy) (*time.Time, error) {
	loc := t.Location()
	now = now.In(loc)
	switch t.Kind {
	case TriggerOnce:
		if t.At == nil {
			return nil, fmt.Errorf("once trigger missing at")
		}
		at := t.At.In(loc)
		policy := ResolveMissed(missed, TriggerOnce)
		if !now.Before(at) {
			if policy == MissedSkip {
				return nil, nil
			}
			// run_once_after_restart (default for once): still due.
			return &at, nil
		}
		return &at, nil
	case TriggerInterval:
		if t.Interval <= 0 {
			return nil, fmt.Errorf("interval must be positive")
		}
		base := now
		if last != nil {
			base = last.In(loc)
		}
		next := base.Add(t.Interval)
		policy := ResolveMissed(missed, TriggerInterval)
		switch policy {
		case MissedSkip:
			for next.Before(now) {
				next = next.Add(t.Interval)
			}
		case MissedRunOnce:
			if next.Before(now) {
				next = now
			}
		case MissedCatchUpAll:
			if next.Before(now) && last != nil {
				next = last.In(loc).Add(t.Interval)
			}
		}
		return &next, nil
	case TriggerCron:
		if strings.TrimSpace(t.Cron) == "" {
			return nil, fmt.Errorf("cron expression is required")
		}
		spec, err := ParseCron(t.Cron)
		if err != nil {
			return nil, err
		}
		policy := ResolveMissed(missed, TriggerCron)
		from := now
		if last != nil && policy == MissedCatchUpAll {
			from = last.In(loc)
		}
		next := spec.Next(from, loc)
		switch policy {
		case MissedSkip:
			if !next.After(now) {
				next = spec.Next(now, loc)
			}
		case MissedRunOnce:
			if next.Before(now) {
				next = now
			}
		}
		return &next, nil
	default:
		return nil, nil
	}
}

// CronSpec is a 5-field cron (minute hour dom month dow).
type CronSpec struct {
	Minute cronField
	Hour   cronField
	Dom    cronField
	Month  cronField
	Dow    cronField
}

type cronField struct {
	all  bool
	vals map[int]struct{}
}

func (f cronField) match(v int) bool {
	if f.all {
		return true
	}
	_, ok := f.vals[v]
	return ok
}

// ParseCron parses a 5-field cron expression.
func ParseCron(expr string) (CronSpec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return CronSpec{}, fmt.Errorf("cron %q: want 5 fields", expr)
	}
	minute, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return CronSpec{}, fmt.Errorf("cron minute: %w", err)
	}
	hour, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return CronSpec{}, fmt.Errorf("cron hour: %w", err)
	}
	dom, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return CronSpec{}, fmt.Errorf("cron day-of-month: %w", err)
	}
	month, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return CronSpec{}, fmt.Errorf("cron month: %w", err)
	}
	dow, err := parseCronField(fields[4], 0, 6)
	if err != nil {
		return CronSpec{}, fmt.Errorf("cron day-of-week: %w", err)
	}
	return CronSpec{Minute: minute, Hour: hour, Dom: dom, Month: month, Dow: dow}, nil
}

func parseCronField(s string, min, max int) (cronField, error) {
	s = strings.TrimSpace(s)
	if s == "*" {
		return cronField{all: true}, nil
	}
	out := cronField{vals: map[int]struct{}{}}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		step := 1
		rangePart := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			rangePart = part[:i]
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return cronField{}, fmt.Errorf("invalid step in %q", part)
			}
			step = n
		}
		var lo, hi int
		if rangePart == "*" {
			lo, hi = min, max
		} else if i := strings.IndexByte(rangePart, '-'); i >= 0 {
			var err error
			lo, err = strconv.Atoi(rangePart[:i])
			if err != nil {
				return cronField{}, err
			}
			hi, err = strconv.Atoi(rangePart[i+1:])
			if err != nil {
				return cronField{}, err
			}
		} else {
			n, err := strconv.Atoi(rangePart)
			if err != nil {
				return cronField{}, err
			}
			lo, hi = n, n
		}
		if lo < min || hi > max || lo > hi {
			return cronField{}, fmt.Errorf("value out of range in %q", part)
		}
		for v := lo; v <= hi; v += step {
			out.vals[v] = struct{}{}
		}
	}
	return out, nil
}

// Next returns the next time strictly after `after` that matches the spec.
func (c CronSpec) Next(after time.Time, loc *time.Location) time.Time {
	t := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(366 * 24 * time.Hour)
	for !t.After(limit) {
		if c.match(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return t
}

func (c CronSpec) match(t time.Time) bool {
	dow := int(t.Weekday()) // Sunday = 0
	return c.Minute.match(t.Minute()) &&
		c.Hour.match(t.Hour()) &&
		c.Dom.match(t.Day()) &&
		c.Month.match(int(t.Month())) &&
		c.Dow.match(dow)
}
