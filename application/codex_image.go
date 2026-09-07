package application

import (
	"context"
	"errors"
	"time"

	"nusashell/domain"
)

// failoverCodexOnImageError switches Codex accounts after an image
// generation failure. Usage-limit 429s (with a provider-reported reset)
// open the account circuit until the quota window resets; plain 429s mark
// the account rate-limited for the usual cooldown; a 403 (no image
// entitlement, e.g. ChatGPT Free) marks the account unusable for a full
// day since plan/entitlement changes are rare. One request per account is
// made: the router blocks the failed account, so a later call picks the
// next available one. Returns newAPIKey + retry=true when another account
// should be tried, replacedErr when no account remains usable. An explicitly
// selected room account is never marked or replaced here: choosing it is a
// strict pin, matching streaming turns.
func (a *App) failoverCodexOnImageError(_ context.Context, conversationID string, provider *domain.Provider, apiKey string, genErr error) (string, bool, error) {
	if a == nil || provider == nil || provider.Kind != domain.ProviderCodex || a.CodexRouter == nil {
		return "", false, nil
	}
	// An explicit composer selection is a strict room-level pin. Only Auto
	// participates in quota/cooldown failover, including image generation.
	if a.selectedCodexAccount(conversationID) != "" {
		return "", false, nil
	}
	var upstream *domain.ProviderError
	if !errors.As(genErr, &upstream) {
		return "", false, nil
	}
	currentAccount := a.CodexRouter.StickyAccount(conversationID)
	if currentAccount == "" {
		// prepareCodexTurnAPIKey was not invoked (or no sticky account yet);
		// without knowing which account failed, failover cannot advance.
		return "", false, nil
	}
	switch {
	case upstream.StatusCode == 429 && !upstream.UsageLimitResetAt.IsZero():
		a.CodexRouter.MarkCircuitOpen(currentAccount, upstream.UsageLimitResetAt)
		a.log("warn", "ai", "codex image circuit open until %s for account %s (image usage limit, conversation %s)",
			upstream.UsageLimitResetAt.Format(time.RFC3339), currentAccount, conversationID)
	case upstream.StatusCode == 429:
		a.CodexRouter.MarkRateLimited(currentAccount, rateLimitCooldown(genErr))
	case upstream.StatusCode == 403:
		a.CodexRouter.MarkCircuitOpen(currentAccount, time.Now().Add(24*time.Hour))
		a.log("warn", "ai", "codex image 403 on account %s (likely no image access, e.g. Free plan) — circuit open 24h (conversation %s)",
			currentAccount, conversationID)
	default:
		return "", false, nil
	}
	accounts := a.listCodexAccountIDs(provider.ID)
	pick := a.CodexRouter.PickAccountDetailed(conversationID, provider.ID, accounts)
	if pick.AccountID != "" && pick.AccountID != currentAccount {
		if token, has, _ := a.Credentials.Get(accountKey(provider.ID, pick.AccountID)); has {
			a.log("info", "ai", "codex image failover: account %s → %s (conversation %s)", currentAccount, pick.AccountID, conversationID)
			return token, true, nil
		}
	}
	if pick.AllRateLimited {
		return "", false, allCodexAccountsLimitedError(pick.EarliestReset)
	}
	return "", false, nil
}
