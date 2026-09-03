package application

import (
	"context"
	"time"
)

// slowDownTick is the polling cadence waitSlowDown uses while a delay is
// active. The live setting is re-read on every tick, so a settings save
// reaches every running conversation within ~tick: lowering the value
// shrinks the current wait, clearing it (0) cancels it outright — no stop,
// no idle turn, no restart. The poll only runs while a delay is nonzero,
// so the default (0) adds no overhead at all.
const slowDownTick = 50 * time.Millisecond

// waitSlowDown pauses the agent before every round while the slow_down
// setting is set. It reads the current setting (not a per-turn snapshot)
// because the whole point is that a save mid-turn takes effect immediately
// on every live conversation. Cancellation (user stop, conversation switch,
// server shutdown) aborts the wait on the next tick.
func (a *App) waitSlowDown(ctx context.Context) {
	if a.Settings == nil {
		return
	}
	delay := time.Duration(a.Settings.Get().SlowDown) * time.Second
	if delay <= 0 {
		return
	}
	deadline := time.Now().Add(delay)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		// A mid-wait settings change takes effect now: a cleared or lower
		// value shortens (or ends) the remaining wait; a higher value does
		// not extend the already-scheduled deadline.
		if cur := time.Duration(a.Settings.Get().SlowDown) * time.Second; cur <= 0 {
			return
		} else if cur < remaining {
			deadline = time.Now().Add(cur)
			remaining = cur
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(slowDownTick):
		}
	}
}
