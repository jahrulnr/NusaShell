package provider

import (
	"context"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/modelcatalog"
)

// This package is the single sanctioned application → infrastructure/ai/core
// import. Root application code must not import core; it talks to providers
// through the types and wrappers re-exported from this package.

// Store is the persistence port for configured chat providers.
type Store interface {
	List() []*domain.Provider
	Get(id string) (*domain.Provider, error)
	Save(p *domain.Provider) error
	Delete(id string) error
}

// Credentials keeps API keys out of the JSON provider records.
type Credentials interface {
	Get(providerID string) (string, bool, error)
	Set(providerID, key string) error
	Delete(providerID string) error
}

// Factory builds a chat adapter for a stored config + key.
type Factory func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error)

// Cataloger is the read-only models.dev catalog used to enrich imported
// models. Duplicated from application.ModelCataloger so this package does
// not import the root application package; App.ModelCatalog assigns.
type Cataloger interface {
	EnsureLoaded(ctx context.Context) error
	Loaded() bool
	Lookup(providerHint, modelID string) *modelcatalog.ModelMetadata
}

// EmbeddingLister enumerates embedding models on a dedicated endpoint.
type EmbeddingLister interface {
	ListEmbeddingModels(ctx context.Context, apiKey string) ([]string, error)
}

// ImageLister enumerates image-generation models.
type ImageLister interface {
	ListImageModels(ctx context.Context, apiKey string) ([]string, error)
}

// SpeechLister enumerates speech-generation models.
type SpeechLister interface {
	ListSpeechModels(ctx context.Context, apiKey string) ([]string, error)
}

// VideoLister enumerates video-generation models.
type VideoLister interface {
	ListVideoModels(ctx context.Context, apiKey string) ([]string, error)
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// ModelAdjuster applies runtime model metadata corrections to the provider
// clone used for a read. The application wires learned context caps and
// manual overrides here so the model window shown by the frontend matches
// the window used by the agent runtime.
type ModelAdjuster func(*domain.Provider, *domain.Model)

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Store                  Store
	Credentials            Credentials
	Factory                Factory
	Catalog                Cataloger
	EmbeddingListerFactory func(*domain.Provider) EmbeddingLister
	ImageListerFactory     func(*domain.Provider) ImageLister
	SpeechListerFactory    func(*domain.Provider) SpeechLister
	VideoListerFactory     func(*domain.Provider) VideoLister
	Log                    Logger
	DataDir                string
	AdjustModel            ModelAdjuster
	OnConfigChanged        func()
	OfflineTTS             func() []contracts.ModelDTO
}

// Service owns provider CRUD, model import, endpoint cache, and env seeding.
type Service struct {
	store                  Store
	credentials            Credentials
	factory                Factory
	catalog                Cataloger
	embeddingListerFactory func(*domain.Provider) EmbeddingLister
	imageListerFactory     func(*domain.Provider) ImageLister
	speechListerFactory    func(*domain.Provider) SpeechLister
	videoListerFactory     func(*domain.Provider) VideoLister
	log                    Logger
	dataDir                string
	adjustModel            ModelAdjuster
	onConfigChanged        func()
	offlineTTS             func() []contracts.ModelDTO
}

// New builds a provider Service from Deps.
func New(d Deps) *Service {
	return &Service{
		store:                  d.Store,
		credentials:            d.Credentials,
		factory:                d.Factory,
		catalog:                d.Catalog,
		embeddingListerFactory: d.EmbeddingListerFactory,
		imageListerFactory:     d.ImageListerFactory,
		speechListerFactory:    d.SpeechListerFactory,
		videoListerFactory:     d.VideoListerFactory,
		log:                    d.Log,
		dataDir:                d.DataDir,
		adjustModel:            d.AdjustModel,
		onConfigChanged:        d.OnConfigChanged,
		offlineTTS:             d.OfflineTTS,
	}
}

func (s *Service) info(format string, args ...any) {
	s.write("info", format, args...)
}

func (s *Service) warn(format string, args ...any) {
	s.write("warn", format, args...)
}

func (s *Service) write(level, format string, args ...any) {
	if s.log != nil {
		s.log(level, "ai", format, args...)
	}
}

func (s *Service) configChanged() {
	if s.onConfigChanged != nil {
		s.onConfigChanged()
	}
}

// AIProvider is the chat provider port. It embeds core.Provider so this
// package can call Chat/Stream directly.
type AIProvider interface {
	core.Provider
}

// ModelLister is implemented by providers that can enumerate models.
type ModelLister interface {
	ListModels(ctx context.Context, apiKey string) ([]domain.Model, error)
}

// ModelEndpointsLister is implemented by aggregator gateways (OpenRouter)
// that can enumerate the upstream providers serving a model.
type ModelEndpointsLister interface {
	ListModelEndpoints(ctx context.Context, slug string) ([]domain.ModelRoute, error)
}

// EmbeddingModelLister is implemented by providers that can enumerate
// embedding models separately from chat models.
type EmbeddingModelLister interface {
	ListEmbeddingModels(ctx context.Context, apiKey string) ([]string, error)
}
