package telemetry

import (
	"nusashell/domain"
)

// ConversationSource projects per-message token usage so reports can
// aggregate without cloning conversation transcripts.
type ConversationSource interface {
	ListUsage() []domain.UsageProjection
}

// ProviderSource lists providers so model pricing can be resolved.
type ProviderSource interface {
	List() []*domain.Provider
}

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Conversations ConversationSource
	Providers     ProviderSource
}

// Service owns the telemetry dashboard RPC surface.
type Service struct {
	conversations ConversationSource
	providers     ProviderSource
}

// New builds a telemetry Service from Deps.
func New(d Deps) *Service {
	return &Service{conversations: d.Conversations, providers: d.Providers}
}
