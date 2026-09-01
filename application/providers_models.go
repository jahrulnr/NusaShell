package application

import (
	"context"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/config"
)

func (a *App) handleModelsList() (any, *contracts.RPCError) {
	// Enrich models with catalog metadata at read time, not just at import.
	// This ensures models imported before a catalog update (or before the
	// suffix-stripping fix) still get reasoning efforts, capabilities, and
	// pricing filled in without requiring a manual re-import.
	if a.ModelCatalog != nil {
		_ = a.ModelCatalog.EnsureLoaded(context.Background())
	}
	var out []contracts.ModelDTO
	for _, p := range a.Providers.List() {
		if !p.Enabled {
			continue
		}
		if a.ModelCatalog != nil && a.ModelCatalog.Loaded() {
			a.enrichProviderModelsAtRead(p)
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
	// Installed offline piper voices surface as speech models in the picker:
	// once the one-click installer finishes, the user can select the voice
	// directly in Settings → Speech generation (provider "piper") instead of
	// relying on the automatic fallback alone.
	out = append(out, offlineTTSModels(a.TTSInstaller)...)
	if out == nil {
		out = []contracts.ModelDTO{}
	}
	return contracts.ModelsListResult{Models: out}, nil
}

// offlineTTSModels maps installed piper voices to speech model entries so
// the Settings model picker shows them after install. Returns nil when no
// installer is wired or nothing is installed.
func offlineTTSModels(inst TTSInstaller) []contracts.ModelDTO {
	if inst == nil {
		return nil
	}
	var out []contracts.ModelDTO
	for _, v := range inst.Status().Voices {
		if !v.Installed {
			continue
		}
		out = append(out, contracts.ModelDTO{
			ID:           v.ID,
			ProviderID:   OfflineTTSProviderID,
			ProviderName: "Offline piper",
			DisplayName:  v.Label,
			Kind:         string(domain.ModelKindTTS),
			TTS:          true,
		})
	}
	return out
}

// enrichProviderModelsAtRead fills in missing metadata from the catalog
// without persisting — it's a read-time enrichment so the UI always shows
// current capabilities even for models imported before a catalog update.
func (a *App) enrichProviderModelsAtRead(p *domain.Provider) {
	for i := range p.Models {
		meta := a.ModelCatalog.Lookup(catalogHintFromModelID(p.Models[i].ID), p.Models[i].ID)
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
		if meta := a.ModelCatalog.Lookup(catalogHintFromModelID(p.Models[i].ID), p.Models[i].ID); meta != nil {
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

// StartAutoModelImport launches a background goroutine that re-imports
// models from all enabled providers every 4 hours. This keeps the model
// list fresh without requiring the user to manually click "import" after
// a provider adds new models. The goroutine exits when ctx is cancelled.
// An initial import runs 30 seconds after startup to avoid blocking the
// server boot.
func (a *App) StartAutoModelImport(ctx context.Context) {
	a.goSafe("ai", func() {
		// Delay the initial import so the server is fully up and serving
		// requests before we start hitting provider APIs.
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		a.autoImportAllProviders(ctx)

		ticker := time.NewTicker(4 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.autoImportAllProviders(ctx)
			}
		}
	})
}

// autoImportAllProviders iterates all enabled providers and re-imports
// their model lists. Failures are logged but do not stop the loop — one
// provider being down should not prevent imports from the others.
func (a *App) autoImportAllProviders(ctx context.Context) {
	for _, p := range a.Providers.List() {
		if !p.Enabled {
			continue
		}
		key, has, _ := a.Credentials.Get(p.ID)
		if !has && domain.RequiresKey(p.Kind) {
			continue
		}
		importCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := a.importModelsForProvider(importCtx, p, key)
		cancel()
		if err != nil {
			a.log("warn", "ai", "auto-import failed: %s: %v", p.Name, err)
		}
	}
}
