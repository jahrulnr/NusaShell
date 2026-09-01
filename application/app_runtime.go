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
// goroutines). Safe to call multiple times. Tests should call this via
// t.Cleanup so that Windows does not fail TempDir removal with "file
// in use" errors from the embedding cache and trajectory log handles.
func (a *App) Close() {
	a.CloseLifecycle()
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
	go func() {
		defer func() {
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

// GoSafe starts a recovered background goroutine. The composition root
// (cmd/nusashell) uses this for fire-and-forget work that must not crash
// the process (same recover as the unexported goSafe helper).
func (a *App) GoSafe(source string, fn func()) {
	a.goSafe(source, fn)
}
