package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/config"
	clock "nusashell/pkg/time"
)

func (a *App) handleProvidersImport(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	p, key, rpcErr := a.providerWithKey(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := a.importModelsForProvider(ctx, p, key)
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
func (a *App) importModelsForProvider(ctx context.Context, p *domain.Provider, key string) ([]domain.Model, error) {
	adapter, err := a.Factory(ctx, p, key)
	if err != nil {
		return nil, err
	}
	lister, ok := adapter.(ModelLister)
	if !ok {
		return nil, fmt.Errorf("provider kind %s does not support model import", p.Kind)
	}
	models, err := lister.ListModels(ctx, key)
	if err != nil {
		a.log("warn", "ai", "model import failed: %s: %v", p.Name, err)
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
	// messages kinds. Skipped if no EmbeddingModelListerFactory is wired.
	if a.EmbeddingModelListerFactory != nil {
		embLister := a.EmbeddingModelListerFactory(p)
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
	if a.ImageModelListerFactory != nil && p.KindCapabilities().HasImageEndpoint {
		imgLister := a.ImageModelListerFactory(p)
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
	if a.SpeechModelListerFactory != nil && p.KindCapabilities().HasSpeechEndpoint {
		spLister := a.SpeechModelListerFactory(p)
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
	if a.VideoModelListerFactory != nil && p.KindCapabilities().HasVideoEndpoint {
		vLister := a.VideoModelListerFactory(p)
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
	if a.ModelCatalog != nil {
		if err := a.ModelCatalog.EnsureLoaded(ctx); err == nil {
			enriched := 0
			for i := range models {
				meta := a.ModelCatalog.Lookup(catalogHintFromModelID(models[i].ID), models[i].ID)
				if meta == nil {
					continue
				}
				isFreeVariant := isFreeTierModel(models[i].ID)
				if models[i].Context == 0 {
					models[i].Context = meta.Context
				}
				if models[i].MaxOutput == 0 {
					models[i].MaxOutput = meta.Output
				}
				// Free-tier variants (e.g. "qwen/qwen3.8-max:free") have $0
				// pricing from the provider API. Don't override with the base
				// model's real pricing from the catalog — $0 is correct.
				if !isFreeVariant {
					if models[i].InputCost == 0 {
						models[i].InputCost = meta.InputCost
					}
					if models[i].OutputCost == 0 {
						models[i].OutputCost = meta.OutputCost
					}
					if models[i].CacheReadCost == 0 {
						models[i].CacheReadCost = meta.CacheReadCost
					}
				}
				if models[i].Description == "" {
					models[i].Description = meta.Description
				}
				if models[i].DisplayName == "" {
					models[i].DisplayName = meta.Name
				}
				if len(models[i].SupportedEfforts) == 0 {
					models[i].SupportedEfforts = meta.SupportedEfforts
				}
				if models[i].KnowledgeCutoff == "" {
					models[i].KnowledgeCutoff = meta.KnowledgeCutoff
				}
				models[i].ToolCall = meta.ToolCall
				models[i].StructuredOutput = meta.StructuredOutput
				models[i].Reasoning = meta.Reasoning
				models[i].Vision = meta.Vision
				models[i].Audio = meta.Audio
				models[i].Video = meta.Video
				models[i].InterleavedField = meta.InterleavedField
				enriched++
			}
			if enriched > 0 {
				a.log("info", "ai", "enriched %d/%d models from models.dev catalog for %s", enriched, len(models), p.Name)
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
		if a.ModelCatalog != nil {
			if meta := a.ModelCatalog.Lookup(catalogHintFromModelID(models[i].ID), models[i].ID); meta != nil {
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
			}
		}
	}
	p.Models = models
	p.UpdatedAt = clock.NewTime().Time()
	if err := a.Providers.Save(p); err != nil {
		return nil, err
	}
	a.log("info", "ai", "imported %d models from %s", len(models), p.Name)
	return models, nil
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

// catalogHintFromModelID derives the catalog hint from a model ID prefix.
// Catalog entries are keyed by vendor prefixes ("deepseek/...", "qwen/...",
// "openai/..."), and the /models API usually emits those prefixes on the
// model ID itself. When the model ID carries no prefix, the hint is empty
// and Lookup falls back to its bare-ID and display-name matching.
func catalogHintFromModelID(modelID string) string {
	lower := strings.ToLower(modelID)
	if idx := strings.Index(lower, "/"); idx > 0 {
		return lower[:idx]
	}
	return ""
}
