package provider

import (
	"context"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
	"nusashell/infrastructure/config"
)

func (s *Service) HandleModelsList() (any, *contracts.RPCError) {
	// Enrich models with catalog metadata at read time, not just at import.
	// This ensures models imported before a catalog update (or before the
	// suffix-stripping fix) still get reasoning efforts, capabilities, and
	// pricing filled in without requiring a manual re-import.
	if s.catalog != nil {
		_ = s.catalog.EnsureLoaded(context.Background())
	}
	var out []contracts.ModelDTO
	if s.store != nil {
		for _, p := range s.store.List() {
			if !p.Enabled {
				continue
			}
			if s.catalog != nil && s.catalog.Loaded() {
				s.enrichProviderModelsAtRead(p)
			}
			// Read-time embedding tagging via the allowlist — runs even without
			// the models.dev catalog so embedding models imported before their
			// name entered the catalog (or before native capability detection)
			// surface in the Embedding model picker without a re-import.
			for i := range p.Models {
				if p.Models[i].Kind == "" && config.IsKnownEmbeddingModel(p.Models[i].ID) {
					p.Models[i].Kind = domain.ModelKindEmbedding
				}
			}
			if s.adjustModel != nil {
				for i := range p.Models {
					s.adjustModel(p, &p.Models[i])
				}
			}
			out = append(out, modelsDTO(p)...)
		}
	}
	// Installed offline piper voices surface as speech models in the picker.
	if s.offlineTTS != nil {
		out = append(out, s.offlineTTS()...)
	}
	if out == nil {
		out = []contracts.ModelDTO{}
	}
	return contracts.ModelsListResult{Models: out}, nil
}

// enrichProviderModelsAtRead fills in missing metadata from the catalog
// without persisting — it's a read-time enrichment so the UI always shows
// current capabilities even for models imported before a catalog update.
func (s *Service) enrichProviderModelsAtRead(p *domain.Provider) {
	catalogHint := catalogProviderHint(p)
	for i := range p.Models {
		meta := s.catalog.Lookup(catalogHint, p.Models[i].ID)
		if meta != nil {
			applyCatalogMetadata(p, &p.Models[i], meta)
		}
		if config.IsKnownImageModel(p.Models[i].ID) {
			p.Models[i].Kind = domain.ModelKindImage
		}
		// Gemini image models (Nano Banana family) accept reference images
		// for editing (image-to-image) by design, but the catalog may not
		// flag every variant as Vision (e.g. nano-banana-pro-preview has no
		// catalog entry). Tag them at read time so the media gate does not
		// block edits. imagen-* is text-to-image only and is excluded by
		// IsGeminiImageI2IModel.
		if p.Models[i].Kind == domain.ModelKindImage && config.IsGeminiImageI2IModel(p.Models[i].ID) {
			p.Models[i].Vision = true
		}
		// TTS/VIDEO/EMBEDDING tagging mirrors the import path: models.dev
		// catalog first (documented carve-outs for speech/video kinds), then
		// the allowlists. Embedding is mirrored here so embedding models
		// imported before the catalog grew (or before Ollama native capability
		// detection existed) surface in Settings → Embedding model without a
		// manual re-import.
		if meta != nil {
			switch meta.Kind {
			case "tts":
				p.Models[i].Kind = domain.ModelKindTTS
			case "video":
				p.Models[i].Kind = domain.ModelKindVideo
			case "embedding":
				p.Models[i].Kind = domain.ModelKindEmbedding
			}
		}
		if config.IsKnownTTSModel(p.Models[i].ID) {
			p.Models[i].Kind = domain.ModelKindTTS
		}
		if config.IsKnownVideoModel(p.Models[i].ID) {
			p.Models[i].Kind = domain.ModelKindVideo
		}
		if p.Models[i].Kind != domain.ModelKindEmbedding && config.IsKnownEmbeddingModel(p.Models[i].ID) {
			p.Models[i].Kind = domain.ModelKindEmbedding
		}
	}
}

func applyCatalogMetadata(p *domain.Provider, m *domain.Model, meta *modelcatalog.ModelMetadata) {
	if p == nil || m == nil || meta == nil {
		return
	}
	isFreeVariant := isFreeTierModel(m.ID)
	m.Context = contextWindowFromCatalog(p.Kind, m.Context, meta.Context)
	if m.MaxOutput == 0 {
		m.MaxOutput = meta.Output
	}
	// Free-tier variants (e.g. "qwen/qwen3.8-max:free") have $0 pricing from
	// the provider API. Don't override with the base model's real pricing.
	if !isFreeVariant {
		if m.InputCost == 0 {
			m.InputCost = meta.InputCost
		}
		if m.OutputCost == 0 {
			m.OutputCost = meta.OutputCost
		}
		if m.CacheReadCost == 0 {
			m.CacheReadCost = meta.CacheReadCost
		}
	}
	if m.Description == "" {
		m.Description = meta.Description
	}
	if m.DisplayName == "" {
		m.DisplayName = meta.Name
	}
	if len(m.SupportedEfforts) == 0 {
		m.SupportedEfforts = meta.SupportedEfforts
	}
	if m.KnowledgeCutoff == "" {
		m.KnowledgeCutoff = meta.KnowledgeCutoff
	}
	// Capabilities are always overridden — the catalog is authoritative
	// for reasoning, tool call, vision, etc.
	m.ToolCall = meta.ToolCall
	m.StructuredOutput = meta.StructuredOutput
	m.Reasoning = meta.Reasoning
	m.Vision = meta.Vision
	m.Audio = meta.Audio
	m.Video = meta.Video
	m.InterleavedField = meta.InterleavedField
	// Capability-only: Kind stays with the lister source, never
	// reclassified by the catalog (matches import path).
}

// ApplyCatalogMetadata is exported for application-level model resolution,
// which must refresh capabilities before learned/manual overrides.
func ApplyCatalogMetadata(p *domain.Provider, m *domain.Model, meta *modelcatalog.ModelMetadata) {
	applyCatalogMetadata(p, m, meta)
}

func modelsDTO(p *domain.Provider) []contracts.ModelDTO {
	models := p.Models
	if p.Kind == domain.ProviderCodex {
		// Display-time seed so ChatGPT plan image models appear even before
		// a fresh import rewrites the persisted provider record.
		models = seedCodexImageModels(append([]domain.Model(nil), models...))
	}
	var out []contracts.ModelDTO
	routeSupport := p.EffectiveDriver() == domain.ProviderDriverOpenRouter || domain.IsOpenRouterHost(p.Kind, p.BaseURL)
	for _, m := range models {
		out = append(out, contracts.ModelDTO{
			ID:               m.ID,
			ProviderID:       p.ID,
			ProviderName:     p.Name,
			ProviderKind:     string(p.Kind),
			DisplayName:      m.DisplayName,
			Context:          m.Context,
			MaxOutput:        m.MaxOutput,
			InputCost:        m.InputCost,
			OutputCost:       m.OutputCost,
			CacheReadCost:    m.CacheReadCost,
			Description:      m.Description,
			SupportedEfforts: m.SupportedEfforts,
			DefaultEffort:    m.DefaultEffort,
			Kind:             string(m.Kind),
			ToolCall:         m.ToolCall,
			StructuredOutput: m.StructuredOutput,
			Reasoning:        m.Reasoning,
			Vision:           m.Vision,
			Audio:            m.Audio,
			Video:            m.Video,
			TTS:              m.Kind == domain.ModelKindTTS,
			VideoGen:         m.Kind == domain.ModelKindVideo,
			KnowledgeCutoff:  m.KnowledgeCutoff,
			RouteSupport:     routeSupport,
		})
	}
	return out
}

// AutoImportAll re-imports models from all enabled providers, first after a
// 30s boot delay and then every 4 hours until ctx is cancelled. The App
// wrapper launches this via goSafe.
func (s *Service) AutoImportAll(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	s.importAllProviders(ctx)

	ticker := time.NewTicker(4 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.importAllProviders(ctx)
		}
	}
}

func (s *Service) importAllProviders(ctx context.Context) {
	if s.store == nil {
		return
	}
	for _, p := range s.store.List() {
		if !p.Enabled {
			continue
		}
		key := ""
		if s.credentials != nil {
			key, _, _ = s.credentials.Get(p.ID)
		}
		importCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := s.importModelsForProvider(importCtx, p, key)
		cancel()
		if err != nil {
			s.warn("auto-import failed: %s: %v", p.Name, err)
		}
	}
}
