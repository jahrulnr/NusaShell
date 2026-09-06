package skills

import (
	"nusashell/domain"
)

// Store is the skill persistence port used by the RPC surface.
type Store interface {
	List() []*domain.Skill
	Get(id, ownedBy string) (*domain.Skill, error)
	Save(s *domain.Skill) error
	Delete(id, ownedBy string) error
	ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error)
	WriteFile(id, ownedBy, path, content string) error
	Files(id, ownedBy string) ([]domain.SkillFileEntry, error)
	Install(zipData []byte) (string, error)
	Promote(id, ownedBy string) (*domain.Skill, error)
	Rollback(id, ownedBy string, version int) (*domain.Skill, error)
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// Emitter publishes skill lifecycle events.
type Emitter interface {
	Emit(typ string, v any)
}

// ChangedHook announces a catalog mutation (install/save/delete) to rooms.
type ChangedHook func(op string)

// LifecycleHook records a promote (or similar) lifecycle event.
type LifecycleHook func(op, id, status string)

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Store     Store
	Log       Logger
	Bus       Emitter
	OnChanged ChangedHook
	OnLife    LifecycleHook
}

// Service owns skills RPC handlers.
type Service struct {
	store     Store
	log       Logger
	bus       Emitter
	onChanged ChangedHook
	onLife    LifecycleHook
}

// New builds a skills Service from Deps.
func New(d Deps) *Service {
	return &Service{
		store:     d.Store,
		log:       d.Log,
		bus:       d.Bus,
		onChanged: d.OnChanged,
		onLife:    d.OnLife,
	}
}

func (s *Service) write(format string, args ...any) {
	if s.log != nil {
		s.log("info", "skills", format, args...)
	}
}

func (s *Service) changed(op string) {
	if s.onChanged != nil {
		s.onChanged(op)
	}
}
