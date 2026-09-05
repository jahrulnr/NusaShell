package application

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// Close releases resources held by the app (file handles, background
// goroutines). Safe to call multiple times. It cancels the lifecycle
// loop, then waits for in-flight learning jobs so tests can remove
// t.TempDir on Windows and Darwin without "directory not empty".
func (a *App) Close() {
	a.CloseLifecycle()
	a.goSafeMu.Lock()
	a.goSafeClosed = true
	a.goSafeMu.Unlock()
	a.goSafeWG.Wait()
	if a.Acp != nil {
		a.Acp.Close()
	}
	if a.EmbeddingCache != nil {
		_ = a.EmbeddingCache.Close()
	}
	if a.Trajectory != nil {
		_ = a.Trajectory.Close()
	}
}

func (a *App) log(level, source, format string, args ...any) {
	e := &domain.LogEntry{
		ID:      domain.NewID(domain.IDPrefixLog),
		Time:    clock.NewTime().Time(),
		Level:   level,
		Source:  source,
		Message: fmt.Sprintf(format, args...),
	}
	if a.Logs != nil {
		a.Logs.Append(e)
	}
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventLogAppend, contracts.LogAppendEvent{Entry: contracts.LogEntryDTO{
			ID: e.ID, Time: clock.NewTime(e.Time).Format(timeRFC3339), Level: e.Level, Source: e.Source, Message: e.Message,
		}})
	}
}

// goSafe runs fn in a new goroutine with panic recovery. A panic is logged
// to both the in-app Logs view (via a.log) and the structured logger (so it
// is visible even when the UI is closed) and does not crash the process.
// Use it for fire-and-forget goroutines whose panic would otherwise take
// down the whole server (agent turns, review agents, background monitors).
func (a *App) goSafe(source string, fn func()) {
	tracked := source == "learning"
	if tracked && !a.beginTrackedGoSafe() {
		return
	}
	go func() {
		defer func() {
			if tracked {
				a.goSafeWG.Done()
			}
			if r := recover(); r != nil {
				stack := debug.Stack()
				a.log("error", source, "goroutine panic recovered: %v\n%s", r, stack)
				logger := a.Logger
				if logger == nil {
					logger = slog.Default()
				}
				logger.Error("goroutine panic recovered", "source", source, "panic", r, "stack", string(stack))
			}
		}()
		fn()
	}()
}

// beginTrackedGoSafe records a learning goroutine so Close can wait for it.
// Returns false when Close has already started draining so WaitGroup is
// never Add-ed after Wait.
func (a *App) beginTrackedGoSafe() bool {
	a.goSafeMu.Lock()
	defer a.goSafeMu.Unlock()
	if a.goSafeClosed {
		return false
	}
	a.goSafeWG.Add(1)
	return true
}

// GoSafe starts a recovered background goroutine. The composition root
// (cmd/nusashell) uses this for fire-and-forget work that must not crash
// the process (same recover as the unexported goSafe helper).
func (a *App) GoSafe(source string, fn func()) {
	a.goSafe(source, fn)
}
