package application

import (
	"context"
	"time"

	"nusashell/application/provider"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

const (
	maxProviderAttempts    = domain.MaxProviderAttempts
	retryBaseDelay         = domain.RetryBaseDelay
	DefaultRateLimitWindow = provider.DefaultRateLimitWindow
)

// RetrySleeper makes the backoff wait deterministic in tests while keeping
// the production retry loop cancellation-aware.
type RetrySleeper func(context.Context, time.Duration) error

func sleepForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *App) waitForRetry(ctx context.Context, delay time.Duration) error {
	sleeper := sleepForRetry
	if a != nil && a.retrySleeper != nil {
		sleeper = a.retrySleeper
	}
	return sleeper(ctx, delay)
}

func (a *App) rateLimits() *provider.RateLimiter {
	if a == nil {
		return provider.NewRateLimiter()
	}
	if a.rateLimiter == nil {
		a.rateLimiter = provider.NewRateLimiter()
	}
	return a.rateLimiter
}

func (a *App) MarkProviderRateLimited(providerID string, nextAllowed time.Time) {
	a.rateLimits().Mark(providerID, nextAllowed)
}

func (a *App) ProviderRateLimitWait(providerID string) time.Duration {
	return a.rateLimits().Wait(providerID)
}

func (a *App) decorateRateLimitError(providerID string, err error) error {
	return a.rateLimits().Decorate(providerID, a.providerNameByID(providerID), err)
}

func (a *App) waitSlowDown(ctx context.Context) {
	if a.Settings == nil {
		return
	}
	provider.WaitSlowDown(ctx, func() int { return a.Settings.Get().SlowDown })
}

func (a *App) providerNameByID(providerID string) string {
	return a.providerService().ProviderName(providerID)
}

func (a *App) friendlyRateLimitError(providerID string, upstream *domain.ProviderError, wait time.Duration) error {
	return provider.FriendlyRateLimitError(a.providerNameByID(providerID), upstream, wait)
}

func (a *App) providerDTO(p *domain.Provider) contracts.ProviderDTO {
	return a.providerService().ProviderDTO(p)
}

func (a *App) enrichProviderModelsAtRead(p *domain.Provider) {
	a.providerService().EnrichModelsAtRead(p)
}

func catalogHintFromModelID(modelID string) string {
	return provider.CatalogHintFromModelID(modelID)
}

func jsonRaw(s string) []byte {
	if s == "" {
		return []byte("{}")
	}
	return []byte(s)
}

func ToCoreRequest(req ChatRequest, kind domain.ProviderKind, openRouter bool) *core.Request {
	return provider.ToCoreRequest(req, kind, openRouter)
}

func FromCoreResponse(resp *core.Response) ChatResponse {
	return provider.FromCoreResponse(resp)
}

func MapCoreError(err error, kind domain.ProviderKind) error {
	return provider.MapCoreError(err, kind)
}

func CompleteViaCore(ctx context.Context, p AIProvider, req ChatRequest, kind domain.ProviderKind, openRouter bool) (ChatResponse, error) {
	return provider.CompleteViaCore(ctx, p, req, kind, openRouter)
}

func StreamViaCore(ctx context.Context, p AIProvider, req ChatRequest, kind domain.ProviderKind, openRouter bool, onDelta, onReasoning func(string)) (ChatResponse, error) {
	return provider.StreamViaCore(ctx, p, req, kind, openRouter, onDelta, onReasoning)
}

func StreamViaCoreWithToolActivity(ctx context.Context, p AIProvider, req ChatRequest, kind domain.ProviderKind, openRouter bool, onDelta, onReasoning func(string), onToolStart func(core.ToolUseStart), onToolDelta func(core.ToolUseDelta)) (ChatResponse, error) {
	return provider.StreamViaCoreWithToolActivity(ctx, p, req, kind, openRouter, onDelta, onReasoning, onToolStart, onToolDelta)
}

func NewProviderContext(p *domain.Provider, adapter AIProvider) ProviderContext {
	return provider.NewProviderContext(p, adapter)
}

func buildPromptCachePolicy(settings domain.Settings, p *domain.Provider, model, conversationID, prefix string) *PromptCachePolicy {
	return provider.BuildPromptCachePolicy(settings, p, model, conversationID, prefix)
}

func buildPromptCachePolicyForContext(settings domain.Settings, adapter ProviderContext, model, conversationID, prefix string) *PromptCachePolicy {
	return provider.BuildPromptCachePolicyForContext(settings, adapter, model, conversationID, prefix)
}

func isRetryableProviderError(err error) bool { return provider.IsRetryableError(err) }
func providerRetryDelay(err error, retry int) (time.Duration, bool) {
	return provider.RetryDelay(err, retry)
}
func describeProviderError(err error) string { return provider.DescribeError(err) }
func isContextOverflowError(err error) bool  { return provider.IsContextOverflow(err) }
func contextLimitFromError(err error) (int, bool) {
	return provider.ContextLimit(err)
}
func shouldEmergencyCompact(err error, estimatedTokens, compactionTrigger int) bool {
	return provider.ShouldEmergencyCompact(err, estimatedTokens, compactionTrigger)
}
func isPrematureStreamEnd(err error) bool { return provider.IsPrematureStreamEnd(err) }

func (a *App) handleProvidersList() (any, *contracts.RPCError) {
	return a.providerService().HandleList()
}
func (a *App) handleProvidersSave(req contracts.ProviderSaveRequest) (any, *contracts.RPCError) {
	return a.providerService().HandleSave(req)
}
func (a *App) handleProvidersDelete(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	name := req.ID
	if a.Providers != nil {
		if p, err := a.Providers.Get(req.ID); err == nil {
			name = p.Name
		}
	}
	resp, rpcErr := a.providerService().HandleDelete(req)
	if rpcErr != nil {
		return nil, rpcErr
	}
	// Codex (and any future multi-account) credentials live under
	// "{providerID}:account:*" and must be removed with the provider.
	a.deleteCodexAccountCredentials(req.ID, name)
	return resp, nil
}
func (a *App) handleProvidersTest(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	return a.providerService().HandleTest(req)
}
func (a *App) handleProvidersImport(req contracts.ProviderIDRequest) (any, *contracts.RPCError) {
	return a.providerService().HandleImport(req)
}
func (a *App) handleModelsList() (any, *contracts.RPCError) {
	return a.providerService().HandleModelsList()
}
func (a *App) handleModelEndpoints(req contracts.ModelEndpointsRequest) (any, *contracts.RPCError) {
	return a.providerService().HandleModelEndpoints(req)
}

func (a *App) SeedProvidersFromEnv(getenv func(string) string) []string {
	return a.providerService().SeedFromEnv(getenv)
}

func (a *App) StartAutoModelImport(ctx context.Context) {
	a.goSafe("ai", func() { a.providerService().AutoImportAll(ctx) })
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
