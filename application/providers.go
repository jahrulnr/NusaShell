package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
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
			ID:           m.ID,
			ProviderID:   p.ID,
			ProviderName: p.Name,
			Context:      m.Context,
			MaxOutput:    m.MaxOutput,
			InputCost:    m.InputCost,
			Description:  m.Description,
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
	if kind != domain.ProviderMessages && kind != domain.ProviderResponses && kind != domain.ProviderChat && kind != domain.ProviderCodex {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "kind must be messages, responses, chat, or codex"}
	}
	// Codex uses a fixed backend URL and OAuth — no user-supplied base URL.
	// Other kinds require a base URL.
	baseURL := strings.TrimSpace(req.BaseURL)
	if kind == domain.ProviderCodex {
		if baseURL == "" {
			baseURL = "https://chatgpt.com/backend-api/codex"
		}
	} else if baseURL == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "base url is required"}
	}

	var p *domain.Provider
	if req.ID != "" {
		existing, err := a.Providers.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		p = existing
		p.Kind = kind
		p.Name = name
		p.BaseURL = baseURL
		p.Enabled = req.Enabled
	} else {
		p = &domain.Provider{
			ID:      domain.NewID("prov"),
			Kind:    kind,
			Name:    name,
			BaseURL: baseURL,
			Enabled: req.Enabled,
		}
	}
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
	_ = a.Credentials.Delete(req.ID)
	a.log("info", "ai", "provider deleted: %s", req.ID)
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
	adapter, err := a.Factory(ctx, p, key)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	lister, ok := adapter.(ModelLister)
	if !ok {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "this provider kind does not support model import"}
	}
	models, err := lister.ListModels(ctx, key)
	if err != nil {
		a.log("warn", "ai", "model import failed: %s: %v", p.Name, err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	if len(models) == 0 {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: "provider returned no models"}
	}
	p.Models = models
	p.UpdatedAt = time.Now().UTC()
	if err := a.Providers.Save(p); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "ai", "imported %d models from %s", len(models), p.Name)
	return contracts.ImportModelsResult{Models: modelsDTO(p)}, nil
}

func (a *App) handleModelsList() (any, *contracts.RPCError) {
	var out []contracts.ModelDTO
	for _, p := range a.Providers.List() {
		if !p.Enabled {
			continue
		}
		out = append(out, modelsDTO(p)...)
	}
	if out == nil {
		out = []contracts.ModelDTO{}
	}
	return contracts.ModelsListResult{Models: out}, nil
}

func modelsDTO(p *domain.Provider) []contracts.ModelDTO {
	var out []contracts.ModelDTO
	for _, m := range p.Models {
		out = append(out, contracts.ModelDTO{
			ID:               m.ID,
			ProviderID:       p.ID,
			ProviderName:     p.Name,
			Context:          m.Context,
			MaxOutput:        m.MaxOutput,
			InputCost:        m.InputCost,
			Description:      m.Description,
			SupportedEfforts: m.SupportedEfforts,
			DefaultEffort:    m.DefaultEffort,
		})
	}
	return out
}
