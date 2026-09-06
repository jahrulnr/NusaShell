package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/rpcdispatch"
	clock "nusashell/pkg/time"
)

// providerNameByID resolves a provider ID to its human-readable name. Falls
// back to the ID itself when the provider is not found (deleted, disabled, or
// never existed) so logs and errors always show something identifiable.
func (s *Service) ProviderName(providerID string) string {
	return s.providerNameByID(providerID)
}

func (s *Service) ProviderDTO(p *domain.Provider) contracts.ProviderDTO {
	return s.providerDTO(p)
}

func (s *Service) EnrichModelsAtRead(p *domain.Provider) {
	if s.catalog == nil || p == nil {
		return
	}
	s.enrichProviderModelsAtRead(p)
}

func (s *Service) providerNameByID(providerID string) string {
	if providerID == "" {
		return "provider"
	}
	if s.store == nil {
		return providerID
	}
	if p, err := s.store.Get(providerID); err == nil && p.Name != "" {
		return p.Name
	}
	return providerID
}

func (s *Service) providerDTO(p *domain.Provider) contracts.ProviderDTO {
	caps := domain.KindCaps(p.Kind)
	driver := p.EffectiveDriver()
	cacheDriver := domain.WireCacheDriver(p.Kind, driver, p.BaseURL)
	ttls := domain.SelectableCacheTTLs(p.Kind, cacheDriver)
	dto := contracts.ProviderDTO{
		ID:         p.ID,
		Driver:     string(driver),
		Kind:       string(p.Kind),
		Name:       p.Name,
		BaseURL:    p.BaseURL,
		Enabled:    p.Enabled,
		CacheTTLs:  append([]string(nil), ttls...),
		CacheStyle: caps.PromptCacheStyle,
	}
	if len(ttls) > 0 {
		dto.CacheTTL = domain.NormalizeCacheTTL(p.Kind, cacheDriver, p.CacheTTL)
	}
	_, hasKey, _ := s.credentials.Get(p.ID)
	dto.HasAPIKey = hasKey
	dto.Configured = hasKey || !domain.RequiresKey(p.Kind)
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

func (s *Service) HandleList() (any, *contracts.RPCError) {
	list := s.store.List()
	out := make([]contracts.ProviderDTO, 0, len(list))
	for _, p := range list {
		out = append(out, s.providerDTO(p))
	}
	return contracts.ProvidersListResult{Providers: out}, nil
}

func (s *Service) HandleSave(req contracts.ProviderSaveRequest) (any, *contracts.RPCError) {
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
		existing, err := s.store.Get(req.ID)
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
			ID:      domain.NewID(domain.IDPrefixProv),
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
	p.UpdatedAt = clock.NewTime().Time()
	cacheDriver := domain.WireCacheDriver(kind, driver, baseURL)
	if req.CacheTTL != nil {
		ttl := strings.TrimSpace(*req.CacheTTL)
		if !domain.ValidCacheTTL(kind, cacheDriver, ttl) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "cache_ttl is not supported for this provider"}
		}
		p.CacheTTL = ttl
	} else if !domain.ValidCacheTTL(kind, cacheDriver, p.CacheTTL) {
		p.CacheTTL = ""
	}

	if req.APIKey != "" {
		if err := s.credentials.Set(p.ID, req.APIKey); err != nil {
			return nil, rpcdispatch.Internal(err)
		}
		p.HasAPIKey = true
	}
	if err := s.store.Save(p); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	s.info("provider saved: %s (%s)", p.Name, p.Kind)
	// Provider changes alter cache keys (provider+model+conversation) and
	// can add/remove tools (web_answer, generate_media): announce globally.
	s.configChanged()
	return contracts.ProvidersListResult{Providers: []contracts.ProviderDTO{s.providerDTO(p)}}, nil
}

func (s *Service) HandleDelete(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	p, err := s.store.Get(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	name := p.Name
	if err := s.store.Delete(req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	if err := s.credentials.Delete(req.ID); err != nil {
		s.warn("failed to delete credential for %s: %v", name, err)
	}
	s.info("provider deleted: %s", name)
	s.configChanged()
	return map[string]bool{"ok": true}, nil
}

func (s *Service) providerWithKey(id string) (*domain.Provider, string, *contracts.RPCError) {
	p, err := s.store.Get(id)
	if err != nil {
		return nil, "", &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	key, _, err := s.credentials.Get(id)
	if err != nil {
		return nil, "", rpcdispatch.Internal(err)
	}
	return p, key, nil
}

// handleProvidersTest probes connectivity only: it lists models via the
// kind-appropriate /models endpoint (responses/chat → /models, messages →
// /v1/models). No completion is sent, so the probe never costs tokens,
// never depends on imported models, and never trips model-routing failures
// on the upstream (a 502 on a specific model stays invisible here and only
// surfaces when actually chatting).
func (s *Service) HandleTest(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	p, key, rpcErr := s.providerWithKey(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adapter, err := s.factory(ctx, p, key)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	lister, ok := adapter.(ModelLister)
	if !ok {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: "this provider kind does not support a connectivity probe"}
	}
	start := clock.NewTime().Time()
	models, err := lister.ListModels(ctx, key)
	if err != nil {
		s.warn("provider test failed: %s [%s, probe /models]: %v", p.Name, p.Kind, err)
		return nil, &contracts.RPCError{
			Code:    contracts.CodeProvider,
			Message: fmt.Sprintf("%s (probe: GET /models)", err.Error()),
		}
	}
	return map[string]any{
		"ok":         true,
		"latency_ms": clock.NewTime().Since(start).Milliseconds(),
		"models":     len(models),
	}, nil
}
