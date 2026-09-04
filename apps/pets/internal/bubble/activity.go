package bubble

import (
	"strings"
	"time"

	"nusashell-pets/internal/state"
)

// DefaultDwell is the minimum time the bubble holds an event before an update
// can switch to a newer one when no explicit dwell is configured.
const DefaultDwell = time.Second

// Activity is owned by the SDL loop. It holds each update for a configurable
// dwell (DefaultDwell when zero) and coalesces bursts to the latest event,
// without delaying pet animations.
type Activity struct {
	dwell   time.Duration
	current state.Event
	pending *state.Event
	since   time.Time
	shownAt time.Time
	header  string
	detail  string
}

// NewActivity creates an activity display with the given minimum dwell.
// Non-positive dwell falls back to DefaultDwell.
func NewActivity(dwell time.Duration) *Activity {
	return &Activity{dwell: dwell}
}

// effectiveDwell returns the minimum hold time, falling back to the default.
func (a *Activity) effectiveDwell() time.Duration {
	if a == nil || a.dwell <= 0 {
		return DefaultDwell
	}
	return a.dwell
}

func (a *Activity) Update(ev state.Event, now time.Time) {
	if !state.Valid(ev.State) {
		return
	}
	if a.current.State == "" || a.current.State == state.StateIdle || now.Sub(a.shownAt) >= a.effectiveDwell() {
		a.current, a.since, a.pending = ev, now, nil
		return
	}
	a.pending = &ev
}

func (a *Activity) Text(now time.Time) (string, string) {
	if a.pending != nil && now.Sub(a.shownAt) >= a.effectiveDwell() {
		a.current, a.since, a.pending = *a.pending, now, nil
	}
	ev := a.current
	var title, detail string
	switch ev.State {
	case state.StateThinking:
		title = "Thinking…"
		phrases := [...]string{"Thinking it through…", "Finding the next step…", "Connecting the dots…", "Taking a closer look…"}
		step := max(0, int(now.Sub(a.since)/(5*time.Second)))
		detail = phrases[step%len(phrases)]
	case state.StateReasoning:
		title, detail = "Reasoning…", "Working through the details…"
	case state.StateWaiting:
		title, detail = "Waiting…", "I need your input to continue."
	case state.StateDone:
		title, detail = "Done", "All set. Take a look!"
	case state.StateError:
		title, detail = "Needs attention", "Check the task for details."
	default:
		a.header, a.detail = "", ""
		return "", ""
	}
	if ev.Title != "" {
		title = ev.Title
	}
	if message := strings.TrimSpace(ev.Message); message != "" {
		detail = message
	}
	if title != a.header || detail != a.detail {
		a.header, a.detail, a.shownAt = title, detail, now
	}
	return a.header, a.detail
}
