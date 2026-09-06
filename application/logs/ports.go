package logs

import (
	"nusashell/domain"
)

// Store is the log-view persistence port. Chronological oldest-first
// listing is the store's contract (see domain/log policy).
type Store interface {
	List(level string, limit int) []*domain.LogEntry
	Clear()
}

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Store Store
}

// Service owns Logs-view RPC handlers.
type Service struct {
	store Store
}

// New builds a logs Service from Deps.
func New(d Deps) *Service {
	return &Service{store: d.Store}
}
