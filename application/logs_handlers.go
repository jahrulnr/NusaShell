package application

import (
	"nusashell/contracts"
	clock "nusashell/pkg/time"
)

func (a *App) handleLogsList(req contracts.LogsListRequest) (any, *contracts.RPCError) {
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	entries := a.Logs.List(req.Level, limit)
	out := make([]contracts.LogEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, contracts.LogEntryDTO{
			ID: e.ID, Time: clock.NewTime(e.Time).Format(timeRFC3339), Level: e.Level, Source: e.Source, Message: e.Message,
		})
	}
	return contracts.LogsListResult{Entries: out}, nil
}

func (a *App) handleLogsClear() (any, *contracts.RPCError) {
	a.Logs.Clear()
	return map[string]bool{"ok": true}, nil
}
