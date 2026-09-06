package provider

import (
	"context"
	"time"
)

// slowDownTick is the polling cadence WaitSlowDown uses while a delay is
// active. The live setting is re-read on every tick, so a settings save
// reaches every running conversation within ~tick: lowering the value
// shrinks the current wait, clearing it (0) cancels it outright — no stop,
// no idle turn, no restart. The poll only runs while a delay is nonzero,
// so the default (0) adds no overhead at all.
const slowDownTick = 50 * time.Millisecond

// WaitSlowDown pauses the agent before every round while slow_down is set.
// getSlowDown returns the current delay in seconds (not a per-turn snapshot)
// because a save mid-turn must take effect immediately on every live
// conversation. Cancellation (user stop, conversation switch, server
// shutdown) aborts the wait on the next tick.
func WaitSlowDown(ctx context.Context, getSlowDown func() int) {
	if getSlowDown == nil {
		return
	}
	delay := time.Duration(getSlowDown()) * time.Second
	if delay <= 0 {
		return
	}
	deadline := time.Now().Add(delay)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		if cur := time.Duration(getSlowDown()) * time.Second; cur <= 0 {
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
