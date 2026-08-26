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

// providerNameByID resolves a provider ID to its human-readable name. Falls
// back to the ID itself when the provider is not found (deleted, disabled, or
// never existed) so logs and errors always show something identifiable.
func (a *App) providerNameByID(providerID string) string {
	if providerID == "" {
		return "provider"
	}
	if a.Providers == nil {
		return providerID
	}
	if p, err := a.Providers.Get(providerID); err == nil && p.Name != "" {
		return p.Name
	}
	return providerID
}

func (a *App) providerDTO(p *domain.Provider) contracts.ProviderDTO {
	dto := contracts.ProviderDTO{
		ID:      p.ID,
		Driver:  string(p.EffectiveDriver()),
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

func builtInProvider(id string) (*domain.Provider, bool) {
	switch id {
	case "anthropic":
		return &domain.Provider{
			ID:      id,
			Driver:  domain.ProviderDriverAnthropic,
			Kind:    domain.ProviderMessages,
			Name:    "Anthropic",
			BaseURL: "https://api.anthropic.com",
			Enabled: true,
		}, true
	case "openai":
		return &domain.Provider{
			ID:      id,
			Driver:  domain.ProviderDriverOpenAI,
			Kind:    domain.ProviderResponses,
			Name:    "OpenAI",
			BaseURL: "https://api.openai.com/v1",
			Enabled: true,
		}, true
	case "openrouter":
		return &domain.Provider{
			ID:      id,
			Driver:  domain.ProviderDriverOpenRouter,
			Kind:    domain.ProviderChat,
			Name:    "OpenRouter",
			BaseURL: "https://openrouter.ai/api/v1",
			Enabled: true,
		}, true
	default:
		return nil, false
	}
}

func validateProviderDriver(driver domain.ProviderDriver, kind domain.ProviderKind) error {
	if !domain.ValidDriver(driver) {
		return fmt.Errorf("unsupported provider driver %q", driver)
	}
	switch driver {
	case domain.ProviderDriverAnthropic:
		if kind != domain.ProviderMessages {
			return fmt.Errorf("anthropic driver only supports messages kind")
		}
	case domain.ProviderDriverOpenAI:
		if kind != domain.ProviderResponses {
			return fmt.Errorf("openai driver only supports responses kind")
		}
	}
	return nil
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
	if kind != domain.ProviderMessages && kind != domain.ProviderResponses && kind != domain.ProviderChat {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "kind must be messages, responses, or chat"}
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	driver := domain.ProviderDriver(strings.ToLower(strings.TrimSpace(req.Driver)))

	var p *domain.Provider
	if req.ID != "" {
		existing, err := a.Providers.Get(req.ID)
		if err != nil {
			if defaults, ok := builtInProvider(req.ID); ok {
				p = defaults
			} else {
				return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
			}
		} else {
			p = existing
		}
	} else {
		p = &domain.Provider{
			ID:      domain.NewID("prov"),
			Kind:    kind,
			Name:    name,
			BaseURL: baseURL,
			Enabled: req.Enabled,
		}
	}

	if driver == domain.ProviderDriverAuto {
		driver = p.EffectiveDriver()
	}
	if err := validateProviderDriver(driver, kind); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}

	// Every supported kind requires a base URL.
	if baseURL == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "base url is required"}
	}
	p.Driver = driver
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
	p, err := a.Providers.Get(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	name := p.Name
	if err := a.Providers.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	if err := a.Credentials.Delete(req.ID); err != nil {
		a.log("warn", "ai", "failed to delete credential for %s: %v", name, err)
	}
	a.log("info", "ai", "provider deleted: %s", name)
	return map[string]bool{"ok": true}, nil
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

// catalogHintForProvider maps a NusaShell provider to a models.dev provider
// ID, used to disambiguate model lookups when the same bare model ID exists
// under multiple providers (e.g. "gpt-5.5" under "openai" and "ai-router").
func catalogHintForProvider(p *domain.Provider) string {
	// Kind-level hints are fixed wire formats, not vendor detection:
	// Responses speaks the OpenAI wire format, Messages speaks the
	// Anthropic one. These are not per-provider hardcodes.
	switch p.Kind {
	case domain.ProviderResponses:
		return "openai"
	case domain.ProviderMessages:
		return "anthropic"
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
		if !has && requiresKey(p.Kind) {
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
