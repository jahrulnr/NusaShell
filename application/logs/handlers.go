package logs

import (
	"nusashell/contracts"
	clock "nusashell/pkg/time"
)

func (s *Service) handleList(req contracts.LogsListRequest) (any, *contracts.RPCError) {
	if s.store == nil {
		return contracts.LogsListResult{Entries: []contracts.LogEntryDTO{}}, nil
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	entries := s.store.List(req.Level, limit)
	out := make([]contracts.LogEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, contracts.LogEntryDTO{
			ID: e.ID, Time: clock.NewTime(e.Time).RFC3339(), Level: e.Level, Source: e.Source, Message: e.Message,
		})
	}
	return contracts.LogsListResult{Entries: out}, nil
}

func (s *Service) handleClear() (any, *contracts.RPCError) {
	if s.store != nil {
		s.store.Clear()
	}
	return map[string]bool{"ok": true}, nil
}
