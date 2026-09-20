package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/modelcatalog"
	"nusashell/infrastructure/config"
	clock "nusashell/pkg/time"
)

func (s *Service) HandleImport(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	p, key, rpcErr := s.providerWithKey(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	timeout := 30 * time.Second
	if p.Kind == domain.ProviderCodex {
		// First import may download the managed Codex CLI binary.
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	models, err := s.importModelsForProvider(ctx, p, key)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	if len(models) == 0 {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: "provider returned no models"}
	}
	return contracts.ImportModelsResult{Models: modelsDTO(p)}, nil
}

// importModelsForProvider fetches the model list from a provider, tags
// embedding models, persists the updated provider, and returns the models.
// Used by both the manual import RPC and the background auto-import ticker.
//
// It fetches chat models via the provider's chat adapter (ModelLister) and
// embedding models via a separate EmbeddingModelLister (if configured). This
// separation is needed because AI gateways often expose embedding models on
// a dedicated /embeddings/models endpoint, separate from the chat /models
// endpoint, and the gateway may be configured with any chat API kind
// (chat, responses, or messages).
func (s *Service) importModelsForProvider(ctx context.Context, p *domain.Provider, key string) ([]domain.Model, error) {
	if !p.KindCapabilities().HasModelListing {
		return nil, fmt.Errorf("provider kind %s does not support model import", p.Kind)
	}
	adapter, err := s.factory(ctx, p, key)
	if err != nil {
		return nil, err
	}
	lister, ok := adapter.(ModelLister)
	if !ok {
		return nil, fmt.Errorf("provider kind %s does not support model import", p.Kind)
	}
	models, err := lister.ListModels(ctx, key)
	if err != nil {
		s.warn("model import failed: %s: %v", p.Name, err)
		return nil, err
	}
	// Tag embedding models from the chat /models response so the learning
	// search layer can pick them. Some gateways (e.g. OpenAI platform) list
	// embedding models in /models directly.
	seen := make(map[string]bool, len(models))
	for i := range models {
		seen[models[i].ID] = true
		if config.IsKnownEmbeddingModel(models[i].ID) {
			models[i].Kind = domain.ModelKindEmbedding
		}
	}
	// Fetch embedding models from the separate /embeddings/models endpoint.
	// This is provider-kind agnostic — works for chat, responses, and
	// messages kinds. Skipped when the kind does not expose embeddings
	// (e.g. Codex OAuth) or no EmbeddingModelListerFactory is wired.
	if s.embeddingListerFactory != nil && p.KindCapabilities().HasEmbeddings {
		embLister := s.embeddingListerFactory(p)
		if embLister != nil {
			embIDs, _ := embLister.ListEmbeddingModels(ctx, key)
			byID := make(map[string]int, len(models))
			for i := range models {
				byID[models[i].ID] = i
			}
			for _, id := range embIDs {
				if i, ok := byID[id]; ok {
					// Already imported via /models — just tag it so it
					// shows up in the Embedding model picker.
					models[i].Kind = domain.ModelKindEmbedding
					continue
				}
				byID[id] = len(models)
				seen[id] = true
				models = append(models, domain.Model{ID: id, Kind: domain.ModelKindEmbedding})
			}
		}
	}
	// Discovery listers for image/speech/video models. These hit dedicated
	// endpoints (OpenRouter's /images/models, /videos/models) or filter
	// queries (?output_modalities=speech) to find model IDs that plain
	// /models hides.
	//
	// The image lister is authoritative for classification: /images/models
	// is a dedicated catalog endpoint (not a filter param), so every ID it
	// returns is an image generator by the endpoint's contract. IDs are
	// tagged Kind=image here, including upgrading an existing /models entry
	// that also appears in the image catalog. This surfaces models the
	// models.dev catalog doesn't carry yet (e.g. krea/krea-2-medium-turbo)
	// without relying on name-pattern allowlists.
	//
	// The speech/video listers stay discovery-only: they use filter params
	// (?output_modalities=speech) that providers may ignore and return their
	// full chat roster, which would misclassify every chat model as TTS
	// (e.g. OpenCode ignores output_modalities=speech). Their IDs keep
	// Kind="" and are classified by the catalog + allowlist pass below.
	if s.imageListerFactory != nil && p.KindCapabilities().HasImageEndpoint {
		imgLister := s.imageListerFactory(p)
		if imgLister != nil {
			imgIDs, _ := imgLister.ListImageModels(ctx, key)
			for _, id := range imgIDs {
				if seen[id] {
					// Upgrade an existing /models entry to image kind.
					for j := range models {
						if models[j].ID == id {
							models[j].Kind = domain.ModelKindImage
							break
						}
					}
					continue
				}
				seen[id] = true
				models = append(models, domain.Model{ID: id, Kind: domain.ModelKindImage})
			}
		}
	}
	if s.speechListerFactory != nil && p.KindCapabilities().HasSpeechEndpoint {
		spLister := s.speechListerFactory(p)
		if spLister != nil {
			spIDs, _ := spLister.ListSpeechModels(ctx, key)
			for _, id := range spIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				models = append(models, domain.Model{ID: id})
			}
		}
	}
	if s.videoListerFactory != nil && p.KindCapabilities().HasVideoEndpoint {
		vLister := s.videoListerFactory(p)
		if vLister != nil {
			vIDs, _ := vLister.ListVideoModels(ctx, key)
			for _, id := range vIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				models = append(models, domain.Model{ID: id})
			}
		}
	}
	// Enrich models with metadata from the models.dev catalog (key-less,
	// public). Fills in context window, pricing, capabilities (reasoning,
	// tool call, structured output, vision) that provider /models endpoints
	// often don't return. Skipped if no catalog is configured or the catalog
	// fetch fails — provider-imported data stays as-is.
	catalogHint := catalogProviderHint(p)
	if s.catalog != nil {
		if err := s.catalog.EnsureLoaded(ctx); err == nil {
			enriched := 0
			for i := range models {
				meta := s.catalog.Lookup(catalogHint, models[i].ID)
				if meta == nil {
					continue
				}
				applyCatalogMetadata(p, &models[i], meta)
				enriched++
			}
			if enriched > 0 {
				s.info("enriched %d/%d models from models.dev catalog for %s", enriched, len(models), p.Name)
			}
		}
	}
	// Classify non-chat kinds from two sources only: the models.dev catalog
	// (meta.Kind) and hardcoded name patterns (IsKnownImageModel,
	// IsKnownTTSModel). Lister endpoints are discovery-only — they find IDs
	// that plain /models hides but never classify them, because providers
	// that ignore filter parameters (e.g. OpenCode ignores
	// output_modalities=speech) would misclassify every chat model as TTS.
	// Unknown models keep Kind="" and appear in the chat picker.
	for i := range models {
		if s.catalog != nil {
			if meta := s.catalog.Lookup(catalogHint, models[i].ID); meta != nil {
				switch meta.Kind {
				case "tts":
					models[i].Kind = domain.ModelKindTTS
				case "video":
					models[i].Kind = domain.ModelKindVideo
				case "image":
					models[i].Kind = domain.ModelKindImage
				case "stt":
					models[i].Kind = domain.ModelKindSTT
				case "embedding":
					models[i].Kind = domain.ModelKindEmbedding
				}
			}
		}
		if models[i].Kind == "" || models[i].Kind == domain.ModelKindChat {
			switch {
			case config.IsKnownImageModel(models[i].ID):
				models[i].Kind = domain.ModelKindImage
			case config.IsKnownTTSModel(models[i].ID):
				models[i].Kind = domain.ModelKindTTS
			case config.IsKnownVideoModel(models[i].ID):
				models[i].Kind = domain.ModelKindVideo
			}
		}
	}
	if p.Kind == domain.ProviderCodex {
		models = seedCodexImageModels(models)
	}
	// Gemini image models (Nano Banana family) accept reference images for
	// editing (image-to-image) by design, but the models.dev catalog has no
	// entry for the AI Studio alias nano-banana-pro-preview and may not flag
	// every gemini-*-image variant as Vision. Tag them here so the media
	// gate (application/media/image_generate.go) does not block edits on
	// i2i-capable models. imagen-* is text-to-image only and is excluded
	// by IsGeminiImageI2IModel.
	for i := range models {
		if models[i].Kind == domain.ModelKindImage && config.IsGeminiImageI2IModel(models[i].ID) {
			models[i].Vision = true
		}
	}
	p.Models = models
	p.UpdatedAt = clock.NewTime().Time()
	if err := s.store.Save(p); err != nil {
		return nil, err
	}
	s.info("imported %d models from %s", len(models), p.Name)
	return models, nil
}

// seedCodexImageModels adds the ChatGPT plan image models that Codex
// model/list does not return. Official Codex imagegen always uses
// gpt-image-2; gpt-image-1.5 is kept for transparent-background workflows.
func seedCodexImageModels(models []domain.Model) []domain.Model {
	seen := make(map[string]int, len(models))
	for i, m := range models {
		seen[m.ID] = i
		if config.IsKnownImageModel(m.ID) {
			models[i].Kind = domain.ModelKindImage
		}
	}
	seeds := []domain.Model{
		// gpt-image-2 / gpt-image-1.5 accept reference images for edit mode,
		// so they are tagged Vision (i2i) — the media gate rejects edits on
		// t2i-only models.
		{ID: "gpt-image-2", DisplayName: "GPT Image 2", Kind: domain.ModelKindImage, Vision: true},
		{ID: "gpt-image-1.5", DisplayName: "GPT Image 1.5", Kind: domain.ModelKindImage, Vision: true},
	}
	for _, seed := range seeds {
		if i, ok := seen[seed.ID]; ok {
			models[i].Kind = domain.ModelKindImage
			models[i].Vision = true
			if strings.TrimSpace(models[i].DisplayName) == "" {
				models[i].DisplayName = seed.DisplayName
			}
			continue
		}
		models = append(models, seed)
	}
	return models
}

// isFreeTierModel reports whether a model ID denotes a free-tier variant.
// Gateways like OpenRouter append ":free" or "-free" to model IDs. These
// variants have $0 pricing from the provider API and should not be
// overridden by the base model's real pricing during catalog enrichment.
func isFreeTierModel(id string) bool {
	lower := strings.ToLower(id)
	for _, suffix := range []string{":free", "-free"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// catalogProviderHint derives the models.dev namespace for the configured
// gateway. This is intentionally provider-derived rather than model-ID
// derived: some gateways expose vendor-qualified IDs ("deepseek/foo"),
// while others expose local IDs ("foo"). In both cases the catalog identity
// is "gateway/model".
func catalogProviderHint(p *domain.Provider) string {
	if p == nil {
		return ""
	}
	if domain.IsOpenCodeHost(p.BaseURL) {
		return "opencode"
	}
	if host := catalogProviderHost(p.BaseURL); host != "" {
		switch {
		case host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai"):
			return "openrouter"
		case host == "anthropic.com" || strings.HasSuffix(host, ".anthropic.com"):
			return "anthropic"
		case host == "openai.com" || strings.HasSuffix(host, ".openai.com"):
			return "openai"
		case host == "generativelanguage.googleapis.com" || host == "aiplatform.googleapis.com":
			return "google"
		}
	}
	switch {
	case p.Kind == domain.ProviderCodex || p.EffectiveDriver() == domain.ProviderDriverCodex:
		return "openai"
	case p.Kind == domain.ProviderGemini || p.EffectiveDriver() == domain.ProviderDriverGemini:
		return "google"
	}
	if id := modelcatalog.NormalizeProviderKey(p.ID); id != "" && !strings.HasPrefix(id, "prov-") {
		return id
	}
	if name := modelcatalog.NormalizeProviderKey(p.Name); name != "" {
		return name
	}
	if host := catalogProviderHost(p.BaseURL); host != "" {
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			return modelcatalog.NormalizeProviderKey(parts[len(parts)-2])
		}
	}
	return ""
}

func catalogProviderHost(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// CatalogProviderHint is exported for application-level catalog lookups.
func CatalogProviderHint(p *domain.Provider) string {
	return catalogProviderHint(p)
}

// contextWindowFromCatalog selects model context metadata without treating
// the local Codex app-server cache as a direct-API limit. Codex chat turns
// use the ChatGPT backend directly, so a catalog value supersedes the
// discovery value (for example, 272000 -> 1050000 for GPT-5.6). Other
// providers retain an already-advertised provider value and only use the
// catalog when the provider did not advertise one.
func contextWindowFromCatalog(kind domain.ProviderKind, current, catalog int) int {
	if catalog <= 0 {
		return current
	}
	if kind == domain.ProviderCodex || current == 0 {
		return catalog
	}
	return current
}

// ContextWindowFromCatalog is exported for application-level model
// resolution, which must apply the same Codex direct-API policy before an
// agent turn starts.
func ContextWindowFromCatalog(kind domain.ProviderKind, current, catalog int) int {
	return contextWindowFromCatalog(kind, current, catalog)
}
