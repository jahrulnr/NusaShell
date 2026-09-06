package provider

import (
	"context"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
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
	for i := range p.Models {
		meta := s.catalog.Lookup(catalogHintFromModelID(p.Models[i].ID), p.Models[i].ID)
		if meta != nil {
			isFreeVariant := isFreeTierModel(p.Models[i].ID)
			if p.Models[i].Context == 0 {
				p.Models[i].Context = meta.Context
			}
			if p.Models[i].MaxOutput == 0 {
				p.Models[i].MaxOutput = meta.Output
			}
			if !isFreeVariant {
				if p.Models[i].InputCost == 0 {
					p.Models[i].InputCost = meta.InputCost
				}
				if p.Models[i].OutputCost == 0 {
					p.Models[i].OutputCost = meta.OutputCost
				}
				if p.Models[i].CacheReadCost == 0 {
					p.Models[i].CacheReadCost = meta.CacheReadCost
				}
			}
			if p.Models[i].Description == "" {
				p.Models[i].Description = meta.Description
			}
			if p.Models[i].DisplayName == "" {
				p.Models[i].DisplayName = meta.Name
			}
			if len(p.Models[i].SupportedEfforts) == 0 {
				p.Models[i].SupportedEfforts = meta.SupportedEfforts
			}
			if p.Models[i].KnowledgeCutoff == "" {
				p.Models[i].KnowledgeCutoff = meta.KnowledgeCutoff
			}
			// Capabilities are always overridden — the catalog is authoritative
			// for reasoning, tool call, vision, etc.
			p.Models[i].ToolCall = meta.ToolCall
			p.Models[i].StructuredOutput = meta.StructuredOutput
			p.Models[i].Reasoning = meta.Reasoning
			p.Models[i].Vision = meta.Vision
			p.Models[i].Audio = meta.Audio
			p.Models[i].Video = meta.Video
			p.Models[i].InterleavedField = meta.InterleavedField
			// Capability-only: Kind stays with the lister source, never
			// reclassified by the catalog (matches import path).
		}
		if config.IsKnownImageModel(p.Models[i].ID) {
			p.Models[i].Kind = domain.ModelKindImage
		}
		// TTS/VIDEO/EMBEDDING tagging mirrors the import path: models.dev
		// catalog first (documented carve-outs for speech/video kinds), then
		// the allowlists. Embedding is mirrored here so embedding models
		// imported before the catalog grew (or before Ollama native capability
		// detection existed) surface in Settings → Embedding model without a
		// manual re-import.
		if meta := s.catalog.Lookup(catalogHintFromModelID(p.Models[i].ID), p.Models[i].ID); meta != nil {
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
		if p.Models[i].Kind != domain.ModelKindEmbedding && config.IsKnownEmbeddingModel(p.Models[i].ID) {
			p.Models[i].Kind = domain.ModelKindEmbedding
		}
	}
}

func modelsDTO(p *domain.Provider) []contracts.ModelDTO {
	models := p.Models
	var out []contracts.ModelDTO
	routeSupport := p.EffectiveDriver() == domain.ProviderDriverOpenRouter || domain.IsOpenRouterHost(p.Kind, p.BaseURL)
	for _, m := range models {
		out = append(out, contracts.ModelDTO{
			ID:               m.ID,
			ProviderID:       p.ID,
			ProviderName:     p.Name,
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
