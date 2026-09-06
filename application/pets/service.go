package pets

import (
	"sync"
)

// Service owns pet RPC handlers, single-flight install, and auto-start.
type Service struct {
	installer Installer
	settings  SettingsSource
	log       Logger
	bus       Emitter
	goFn      GoFunc

	installMu     sync.Mutex
	installActive bool
}

// New builds a pets Service from Deps.
func New(d Deps) *Service {
	goFn := d.Go
	if goFn == nil {
		goFn = func(fn func()) { go fn() }
	}
	return &Service{
		installer: d.Installer,
		settings:  d.Settings,
		log:       d.Log,
		bus:       d.Bus,
		goFn:      goFn,
	}
}

func (s *Service) write(level, format string, args ...any) {
	if s.log != nil {
		s.log(level, "pets", format, args...)
	}
}

func (s *Service) emit(typ string, v any) {
	if s.bus != nil {
		s.bus.Emit(typ, v)
	}
}

func (s *Service) beginInstall() bool {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	if s.installActive {
		return false
	}
	s.installActive = true
	return true
}

func (s *Service) endInstall() {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	s.installActive = false
}

// InstallRunning reports the single-flight install slot.
func (s *Service) InstallRunning() bool {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	return s.installActive
}
