package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/config"
)

func (a *App) providerDTO(p *domain.Provider) contracts.ProviderDTO {
	dto := contracts.ProviderDTO{
		ID:      p.ID,
		Kind:    string(p.Kind),
		Name:    p.Name,
		BaseURL: p.BaseURL,
		Enabled: p.Enabled,
	}
	_, hasKey, _ := a.Credentials.Get(p.ID)
	dto.HasAPIKey = hasKey
	dto.Configured = hasKey || !requiresKey(p.Kind)
	for _, m := range p.Models {
		dto.Models = append(dto.Models, contracts.ModelDTO{
			ID:               m.ID,
			ProviderID:       p.ID,
			ProviderName:     p.Name,
			Context:          m.Context,
			MaxOutput:        m.MaxOutput,
			InputCost:        m.InputCost,
			Description:      m.Description,
			SupportedEfforts: m.SupportedEfforts,
			DefaultEffort:    m.DefaultEffort,
			Kind:             string(m.Kind),
		})
	}
	return dto
}

func (a *App) handleProvidersList() (any, *contracts.RPCError) {
	list := a.Providers.List()
	out := make([]contracts.ProviderDTO, 0, len(list))
	for _, p := range list {
		out = append(out, a.providerDTO(p))
	}
	return contracts.ProvidersListResult{Providers: out}, nil
}

func (a *App) handleProvidersSave(req contracts.ProviderSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "provider name is required"}
	}
	kind := domain.ProviderKind(strings.ToLower(strings.TrimSpace(req.Kind)))
	if kind == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "kind is required"}
	}
	if kind != domain.ProviderMessages && kind != domain.ProviderResponses && kind != domain.ProviderChat && kind != domain.ProviderOllama && kind != domain.ProviderCodex {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "kind must be messages, responses, chat, ollama, or codex"}
	}
	baseURL := strings.TrimSpace(req.BaseURL)

	// Resolve the existing provider first: the kind-change guard depends on
	// it and must fire before base URL validation (switching a codex
	// provider to "chat" would otherwise fail on "base url is required"
	// before reaching the guard).
	var p *domain.Provider
	if req.ID != "" {
		existing, err := a.Providers.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		// Codex is provider-specific: OAuth accounts and the fixed backend
		// URL are tied to the kind, so switching it in place would orphan
		// that state. Delete and recreate instead.
		if existing.Kind == domain.ProviderCodex && kind != domain.ProviderCodex {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "codex kind is provider-specific and cannot be changed; delete this provider and add a new one instead"}
		}
		p = existing
	} else {
		p = &domain.Provider{
			ID:      domain.NewID("prov"),
			Kind:    kind,
			Name:    name,
			BaseURL: baseURL,
			Enabled: req.Enabled,
		}
	}

	// Codex uses a fixed backend URL and OAuth — no user-supplied base URL.
	// Ollama defaults to localhost:11434 — no API key needed.
	// Other kinds require a base URL.
	if baseURL == "" {
		switch kind {
		case domain.ProviderCodex:
			baseURL = "https://chatgpt.com/backend-api/codex"
		case domain.ProviderOllama:
			baseURL = "http://localhost:11434"
		default:
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "base url is required"}
		}
	}
	p.Kind = kind
	p.Name = name
	p.BaseURL = baseURL
	p.Enabled = req.Enabled
	p.UpdatedAt = time.Now().UTC()

	if req.APIKey != "" {
		if err := a.Credentials.Set(p.ID, req.APIKey); err != nil {
			return nil, rpcInternal(err)
		}
		p.HasAPIKey = true
	}
	if err := a.Providers.Save(p); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "ai", "provider saved: %s (%s)", p.Name, p.Kind)
	return contracts.ProvidersListResult{Providers: []contracts.ProviderDTO{a.providerDTO(p)}}, nil
}

func (a *App) handleProvidersDelete(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	if _, err := a.Providers.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := a.Providers.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	a.deleteProviderCredentials(req.ID)
	a.log("info", "ai", "provider deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

func (a *App) deleteProviderCredentials(providerID string) {
	if err := a.Credentials.Delete(providerID); err != nil {
		a.log("warn", "ai", "failed to delete credential %s: %v", providerID, err)
	}
	ids, err := a.Credentials.ListByPrefix(accountKeyPrefix(providerID))
	if err != nil {
		a.log("warn", "ai", "failed to list account credentials for %s: %v", providerID, err)
		return
	}
	for _, id := range ids {
		if err := a.Credentials.Delete(id); err != nil {
			a.log("warn", "ai", "failed to delete credential %s: %v", id, err)
		}
	}
}

func (a *App) providerWithKey(id string) (*domain.Provider, string, *contracts.RPCError) {
	p, err := a.Providers.Get(id)
	if err != nil {
		return nil, "", &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	key, has, err := a.Credentials.Get(id)
	if err != nil {
		return nil, "", rpcInternal(err)
	}
	if !has && requiresKey(p.Kind) {
		return nil, "", &contracts.RPCError{Code: contracts.CodeConflict, Message: "provider has no API key configured"}
	}
	return p, key, nil
}

// handleProvidersTest probes connectivity only: it lists models via the
// kind-appropriate /models endpoint (responses/chat → /models, messages →
// /v1/models). No completion is sent, so the probe never costs tokens,
// never depends on imported models, and never trips model-routing failures
// on the upstream (a 502 on a specific model stays invisible here and only
// surfaces when actually chatting).
func (a *App) handleProvidersTest(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	p, key, rpcErr := a.providerWithKey(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adapter, err := a.Factory(ctx, p, key)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	lister, ok := adapter.(ModelLister)
	if !ok {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: "this provider kind does not support a connectivity probe"}
	}
	start := time.Now()
	models, err := lister.ListModels(ctx, key)
	if err != nil {
		a.log("warn", "ai", "provider test failed: %s [%s, probe /models]: %v", p.Name, p.Kind, err)
		return nil, &contracts.RPCError{
			Code:    contracts.CodeProvider,
			Message: fmt.Sprintf("%s (probe: GET /models)", err.Error()),
		}
	}
	return map[string]any{
		"ok":         true,
		"latency_ms": time.Since(start).Milliseconds(),
		"models":     len(models),
	}, nil
}

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
	// messages kinds. Skipped if no EmbeddingModelListerFactory is wired or
	// the provider kind does not expose the endpoint (e.g. Codex OAuth).
	if a.EmbeddingModelListerFactory != nil && p.Kind != domain.ProviderCodex {
		embLister := a.EmbeddingModelListerFactory(p)
		if embLister != nil {
			embIDs, _ := embLister.ListEmbeddingModels(ctx, key)
			for _, id := range embIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				models = append(models, domain.Model{ID: id, Kind: domain.ModelKindEmbedding})
			}
		}
	}
	// Fetch image-generation models from GET /images/models (OpenRouter).
	// OpenAI hosts 404 this endpoint; known ids from /models are still
	// tagged after catalog enrichment below.
	imageSet := map[string]bool{}
	if a.ImageModelListerFactory != nil && p.Kind != domain.ProviderCodex && p.Kind != domain.ProviderMessages {
		imgLister := a.ImageModelListerFactory(p)
		if imgLister != nil {
			imgIDs, _ := imgLister.ListImageModels(ctx, key)
			for _, id := range imgIDs {
				imageSet[id] = true
				if seen[id] {
					continue
				}
				seen[id] = true
				models = append(models, domain.Model{ID: id, Kind: domain.ModelKindImage})
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
				// The catalog assigns capabilities only; it must never
				// reclassify a model. Kind is decided by the source that
				// listed the model (/models, /embeddings/models,
				// /images/models, or Codex seeds) — never by models.dev.
				// Keeping Kind out of the catalog keeps the provider list
				// faithful to the /models API (as-is).
				enriched++
			}
			if enriched > 0 {
				a.log("info", "ai", "enriched %d/%d models from models.dev catalog for %s", enriched, len(models), p.Name)
			}
		}
	}
	// Re-apply image kind after catalog enrichment — models.dev can mark
	// gpt-image-* as chat when output modalities are missing.
	for i := range models {
		if imageSet[models[i].ID] || config.IsKnownImageModel(models[i].ID) {
			models[i].Kind = domain.ModelKindImage
		}
	}
	if p.Kind == domain.ProviderCodex {
		models = seedCodexImageModels(models)
	}
	p.Models = models
	p.UpdatedAt = time.Now().UTC()
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
		{ID: "gpt-image-2", DisplayName: "GPT Image 2", Kind: domain.ModelKindImage},
		{ID: "gpt-image-1.5", DisplayName: "GPT Image 1.5", Kind: domain.ModelKindImage},
	}
	for _, seed := range seeds {
		if i, ok := seen[seed.ID]; ok {
			models[i].Kind = domain.ModelKindImage
			if strings.TrimSpace(models[i].DisplayName) == "" {
				models[i].DisplayName = seed.DisplayName
			}
			continue
		}
		models = append(models, seed)
	}
	return models
}

// catalogHintForProvider maps a NusaShell provider to a models.dev provider
// ID, used to disambiguate model lookups when the same bare model ID exists
// under multiple providers (e.g. "gpt-5.5" under "openai" and "ai-router").
func catalogHintForProvider(p *domain.Provider) string {
	// Kind-level hints are fixed wire formats, not vendor detection:
	// Responses/Codex speak the OpenAI wire format, Messages speaks the
	// Anthropic one. These are not per-provider hardcodes.
	switch p.Kind {
	case domain.ProviderResponses:
		return "openai"
	case domain.ProviderMessages:
		return "anthropic"
	case domain.ProviderCodex:
		return "openai"
	}
	// Chat-kind providers are fully dynamic: the hint is derived from the
	// model ID prefix itself (e.g. "deepseek/deepseek-v4-flash" ->
	// "deepseek"), which is the output of the /models API. No provider
	// registry, no base-URL sniffing, no vendor hardcodes — any new
	// provider (TokenRouter, OpenRouter, a future gateway) enriches
	// automatically.
	return ""
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
		out = append(out, modelsDTO(p)...)
	}
	if out == nil {
		out = []contracts.ModelDTO{}
	}
	return contracts.ModelsListResult{Models: out}, nil
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
			// Capability-only: Kind stays with the lister source, never
			// reclassified by the catalog (matches import path).
		}
		if config.IsKnownImageModel(p.Models[i].ID) {
			p.Models[i].Kind = domain.ModelKindImage
		}
	}
}

func modelsDTO(p *domain.Provider) []contracts.ModelDTO {
	models := p.Models
	if p.Kind == domain.ProviderCodex {
		models = seedCodexImageModels(append([]domain.Model(nil), models...))
	}
	var out []contracts.ModelDTO
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
			KnowledgeCutoff:  m.KnowledgeCutoff,
		})
	}
	return out
}
