package application

import (
	"context"
	"fmt"
	"time"

	"nusashell/domain"
)

// prepareCodexTurnAPIKey selects the sticky Codex account token for a
// conversation before the first provider request. Non-Codex providers and
// nil routers leave apiKey unchanged.
func (a *App) prepareCodexTurnAPIKey(conversationID string, provider *domain.Provider, apiKey string) (string, error) {
	if a == nil || provider == nil || provider.Kind != domain.ProviderCodex || a.CodexRouter == nil {
		return apiKey, nil
	}
	accounts := a.listCodexAccountIDs(provider.ID)
	if len(accounts) == 0 {
		return apiKey, nil
	}
	pick := a.CodexRouter.PickAccountDetailed(conversationID, provider.ID, accounts)
	if pick.AccountID != "" {
		if token, has, _ := a.Credentials.Get(accountKey(provider.ID, pick.AccountID)); has {
			return token, nil
		}
		return apiKey, nil
	}
	if pick.AllRateLimited {
		return "", allCodexAccountsLimitedError(pick.EarliestReset)
	}
	return apiKey, nil
}

// failoverCodexOnStreamError marks the sticky account rate-limited or
// circuit-open on 429 and switches to another account when available.
func (a *App) failoverCodexOnStreamError(ctx context.Context, conversationID string, provider *domain.Provider, apiKey string, streamErr error) (string, bool, error) {
	if a == nil || provider == nil || provider.Kind != domain.ProviderCodex || a.CodexRouter == nil || !isRateLimitError(streamErr) {
		return "", false, nil
	}
	currentAccount := a.CodexRouter.StickyAccount(conversationID)
	cooldown := rateLimitCooldown(streamErr)
	if cooldown > retryAfterCutoff {
		a.CodexRouter.MarkCircuitOpen(currentAccount, time.Now().Add(cooldown))
		a.log("warn", "ai", "codex circuit open: account %s usage exhausted for %s (conversation %s)",
			currentAccount, cooldown.Round(time.Minute), conversationID)
		a.goSafe("codex", func() {
			a.refreshCodexCircuit(context.WithoutCancel(ctx), provider.ID, currentAccount)
		})
	} else {
		a.CodexRouter.MarkRateLimited(currentAccount, cooldown)
	}
	accounts := a.listCodexAccountIDs(provider.ID)
	pickResult := a.CodexRouter.PickAccountDetailed(conversationID, provider.ID, accounts)
	newAccount := pickResult.AccountID
	if newAccount != "" && newAccount != currentAccount {
		if newToken, has, _ := a.Credentials.Get(accountKey(provider.ID, newAccount)); has {
			a.log("info", "ai", "codex failover: account %s → %s for conversation %s",
				currentAccount, newAccount, conversationID)
			return newToken, true, nil
		}
	}
	if pickResult.AllRateLimited {
		return "", false, allCodexAccountsLimitedError(pickResult.EarliestReset)
	}
	return "", false, nil
}

func allCodexAccountsLimitedError(reset time.Time) error {
	if reset.IsZero() {
		return fmt.Errorf("all Codex accounts are rate-limited")
	}
	return fmt.Errorf("all Codex accounts are rate-limited. Earliest reset at %s", reset.Format(time.RFC3339))
}
