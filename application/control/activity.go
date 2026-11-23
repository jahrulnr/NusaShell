package appcontrol

import "time"

// activityCap bounds the cross-thread activity log — enough history to
// answer "who poked my threads" without growing memory.
const activityCap = 500

// ActivityEntry is one recorded cross-thread tool call. It carries
// metadata only — never message text or tool arguments.
type ActivityEntry struct {
	At         time.Time `json:"at"`
	Caller     string    `json:"caller"`
	Tool       string    `json:"tool"`
	Target     string    `json:"target,omitempty"`
	Outcome    string    `json:"outcome"`
	DurationMs int64     `json:"durationMs"`
}

// ActivityFilter narrows Activity results. Empty Caller/Target match
// everything; Limit caps the newest-first result (0 = all).
type ActivityFilter struct {
	Caller string
	Target string
	Limit  int
}

// note records one tool call in the ring. outcome is "ok" or "error" —
// the error's text stays with the caller, never in the log.
func (a *App) note(caller, tool, target string, start time.Time, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	a.mu.Lock()
	a.activity = append(a.activity, ActivityEntry{
		At: start, Caller: caller, Tool: tool, Target: target,
		Outcome: outcome, DurationMs: time.Since(start).Milliseconds(),
	})
	if len(a.activity) > activityCap {
		a.activity = a.activity[len(a.activity)-activityCap:]
	}
	a.mu.Unlock()
}

// Activity returns entries newest-first, filtered by caller and/or
// target thread id.
func (a *App) Activity(f ActivityFilter) []ActivityEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ActivityEntry, 0, min(len(a.activity), 64))
	for i := len(a.activity) - 1; i >= 0; i-- {
		e := a.activity[i]
		if f.Caller != "" && e.Caller != f.Caller {
			continue
		}
		if f.Target != "" && e.Target != f.Target {
			continue
		}
		out = append(out, e)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out
}

// Clear empties the log — called after a delete-all so entries stop
// pointing at threads that no longer exist.
func (a *App) Clear() {
	a.mu.Lock()
	a.activity = nil
	a.mu.Unlock()
}
