package settings

import (
	"nusashell/domain"
)

// Store is the settings persistence port.
type Store interface {
	Get() domain.Settings
	Set(s domain.Settings) error
}

// ApplyStore is the optional hot-swap capability used by the file watcher
// so disk reloads do not write the file back.
type ApplyStore interface {
	ApplySettings(s domain.Settings)
}

// Credential is the write-only API-key port used by settings.set.
type Credential interface {
	Set(providerID, key string) error
	Delete(providerID string) error
}

// AppliedHook runs after a successful settings.set. The root App uses it
// to invalidate learning search and announce user-prompt changes without
// this package importing those subsystems.
type AppliedHook func(oldUserPrompt string, next domain.Settings)

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// Emitter publishes application events (settings.applied / rejected).
type Emitter interface {
	Emit(typ string, v any)
}

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Store       Store
	Credentials Credential
	OnApplied   AppliedHook
}

// Service owns settings RPC handlers (get/set). TTS/STT install stays on
// the root App until the media subsystem moves.
type Service struct {
	store     Store
	creds     Credential
	onApplied AppliedHook
}

// New builds a settings Service from Deps.
func New(d Deps) *Service {
	return &Service{store: d.Store, creds: d.Credentials, onApplied: d.OnApplied}
}
