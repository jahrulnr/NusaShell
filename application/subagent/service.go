package subagent

import (
	"sync"

	"nusashell/domain"
)

// Service owns ACP subagent RPC, spawn, persist, and the internal delegate
// run registry. Transcript injection that needs TurnRun stays on App via
// DeliverRunDone / CompleteSubagent / CompleteDelegate.
type Service struct {
	deps Deps

	delegateRunsMu sync.RWMutex
	delegateRuns   map[string]*domain.AcpRun
}

// New builds a subagent Service from Deps.
func New(d Deps) *Service {
	goFn := d.Go
	if goFn == nil {
		goFn = func(_ string, fn func()) { go fn() }
	}
	d.Go = goFn
	return &Service{
		deps:         d,
		delegateRuns: map[string]*domain.AcpRun{},
	}
}

func (s *Service) log(level, source, format string, args ...any) {
	if s != nil && s.deps.Log != nil {
		s.deps.Log(level, source, format, args...)
	}
}

func (s *Service) emit(typ string, v any) {
	if s != nil && s.deps.Bus != nil {
		s.deps.Bus.Emit(typ, v)
	}
}

func (s *Service) goSafe(name string, fn func()) {
	if s == nil || s.deps.Go == nil {
		go fn()
		return
	}
	s.deps.Go(name, fn)
}

func (s *Service) notifyAgentsChanged() {
	if s != nil && s.deps.OnAgentsChanged != nil {
		s.deps.OnAgentsChanged()
	}
}

func (s *Service) trackPending(conversationID, runID, tool string) {
	if s != nil && s.deps.TrackPending != nil {
		s.deps.TrackPending(conversationID, runID, tool)
	}
}
