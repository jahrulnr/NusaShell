package valueobject

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ScheduleKind is how a schedule repeats.
type ScheduleKind string

const (
	// ScheduleOnce fires at one moment.
	ScheduleOnce ScheduleKind = "once"
	// ScheduleCron fires on a five-field cron expression.
	ScheduleCron ScheduleKind = "cron"
)

// Schedule is when something fires, read in an IANA time zone: once at a
// moment, or on a standard five-field cron expression (minute, hour, day
// of month, month, day of week).
type Schedule struct {
	kind ScheduleKind
	at   time.Time
	cron string
	zone string
	spec cron.Schedule
}

// NewOnceSchedule fires once at at. zone names the time zone the moment
// was picked in, for display.
func NewOnceSchedule(at time.Time, zone string) (Schedule, error) {
	if at.IsZero() {
		return Schedule{}, errors.New("a one-time schedule needs a time")
	}
	loc, err := loadZone(zone)
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{kind: ScheduleOnce, at: at.In(loc), zone: loc.String()}, nil
}

// NewCronSchedule fires on a five-field cron expression read in zone.
func NewCronSchedule(expr, zone string) (Schedule, error) {
	expr = strings.Join(strings.Fields(expr), " ")
	if len(strings.Fields(expr)) != 5 {
		return Schedule{}, errors.New("a cron expression has five fields: minute hour day-of-month month day-of-week")
	}
	loc, err := loadZone(zone)
	if err != nil {
		return Schedule{}, err
	}
	spec, err := cron.ParseStandard("CRON_TZ=" + loc.String() + " " + expr)
	if err != nil {
		return Schedule{}, fmt.Errorf("invalid cron expression: %w", err)
	}
	if spec.Next(time.Now()).IsZero() {
		return Schedule{}, errors.New("the cron expression never fires")
	}
	return Schedule{kind: ScheduleCron, cron: expr, zone: loc.String(), spec: spec}, nil
}

func loadZone(zone string) (*time.Location, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return nil, errors.New("a schedule needs a time zone")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", zone)
	}
	return loc, nil
}

func (s Schedule) Kind() ScheduleKind { return s.kind }
func (s Schedule) At() time.Time      { return s.at }
func (s Schedule) Cron() string       { return s.cron }
func (s Schedule) Zone() string       { return s.zone }
func (s Schedule) IsZero() bool       { return s.kind == "" }

// Next is the first firing after after; zero when the schedule has no
// more. A one-time schedule fires at its moment even when that has
// passed, as long as it is after after — so a run missed while the server
// was down still happens once.
func (s Schedule) Next(after time.Time) time.Time {
	switch s.kind {
	case ScheduleOnce:
		if s.at.After(after) {
			return s.at
		}
	case ScheduleCron:
		return s.spec.Next(after)
	}
	return time.Time{}
}

// Same reports whether two schedules fire identically.
func (s Schedule) Same(o Schedule) bool {
	return s.kind == o.kind && s.at.Equal(o.at) && s.cron == o.cron && s.zone == o.zone
}
