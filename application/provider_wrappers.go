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

func NewProviderContext(p *domain.Provider, adapter AIProvider) ProviderContext {
	return provider.NewProviderContext(p, adapter)
}

// ToCoreRequest and MapCoreError stay exported here because the provider
// adapter tests in infrastructure/ai exercise the application→core bridge
// through this package (see infrastructure/ai/adapter_test.go). The other
// root-level core bridges were deleted with the dead wrapper sweep.
func ToCoreRequest(req ChatRequest, kind domain.ProviderKind, openRouter bool) *core.Request {
	return provider.ToCoreRequest(req, kind, openRouter)
}

func MapCoreError(err error, kind domain.ProviderKind) error {
	return provider.MapCoreError(err, kind)
}

func providerRetryDelay(err error, retry int) (time.Duration, bool) {
	return provider.RetryDelay(err, retry)
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
