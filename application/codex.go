package application

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"nusashell/contracts"
)

// accountKeyPrefix returns the CredentialStore key prefix for additional
// accounts of a Codex provider: "{providerID}:account:".
func accountKeyPrefix(providerID string) string {
	return providerID + ":account:"
}

// accountKey returns the CredentialStore key for a specific account.
func accountKey(providerID, accountID string) string {
	return accountKeyPrefix(providerID) + accountID
}

// handleCodexLogin triggers the OAuth PKCE flow. The implementation opens
// a browser and blocks until the callback completes. The resulting token
// is stored in CredentialStore under both the provider ID (active) and
// the account-specific key (for multi-account support).
func (a *App) handleCodexLogin(req contracts.CodexLoginRequest) (any, *contracts.RPCError) {
	if _, err := a.Providers.Get(req.ProviderID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if a.CodexOAuth == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex OAuth is not configured"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tok, err := a.CodexOAuth.Login(ctx)
	if err != nil {
		a.log("warn", "ai", "codex login failed: %v", err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	// Marshal token as JSON for storage
	tokenJSON, err := json.Marshal(CodexTokenJSON{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		AccountID:    tok.AccountID,
		Email:        tok.Email,
		Name:         tok.Name,
		ExpiresAt:    tok.ExpiresAt,
	})
	if err != nil {
		return nil, rpcInternal(err)
	}
	tokenStr := string(tokenJSON)
	// Store under account-specific key
	if tok.AccountID != "" {
		if err := a.Credentials.Set(accountKey(req.ProviderID, tok.AccountID), tokenStr); err != nil {
			return nil, rpcInternal(err)
		}
	}
	// Set as active
	if err := a.Credentials.Set(req.ProviderID, tokenStr); err != nil {
		return nil, rpcInternal(err)
	}
	// Mark provider as having credentials
	p, _ := a.Providers.Get(req.ProviderID)
	p.HasAPIKey = true
	p.UpdatedAt = time.Now().UTC()
	_ = a.Providers.Save(p)
	a.log("info", "ai", "codex login successful: account %s", tok.AccountID)
	return contracts.CodexLoginResult{AccountID: tok.AccountID}, nil
}

// handleCodexLogout removes a stored OAuth token. If accountID is empty,
// the active account is removed.
func (a *App) handleCodexLogout(req contracts.CodexLogoutRequest) (any, *contracts.RPCError) {
	if _, err := a.Providers.Get(req.ProviderID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if req.AccountID != "" {
		_ = a.Credentials.Delete(accountKey(req.ProviderID, req.AccountID))
		// If this was the active account, clear the active key too
		activeJSON, hasActive, _ := a.Credentials.Get(req.ProviderID)
		if hasActive {
			var active CodexTokenJSON
			if json.Unmarshal([]byte(activeJSON), &active) == nil && active.AccountID == req.AccountID {
				_ = a.Credentials.Delete(req.ProviderID)
				// Mark provider as not configured
				p, _ := a.Providers.Get(req.ProviderID)
				p.HasAPIKey = false
				p.UpdatedAt = time.Now().UTC()
				_ = a.Providers.Save(p)
			}
		}
	} else {
		_ = a.Credentials.Delete(req.ProviderID)
		p, _ := a.Providers.Get(req.ProviderID)
		p.HasAPIKey = false
		p.UpdatedAt = time.Now().UTC()
		_ = a.Providers.Save(p)
	}
	a.log("info", "ai", "codex logout: %s", req.AccountID)
	return map[string]bool{"ok": true}, nil
}

// handleCodexAccountsList enumerates all stored OAuth accounts for a Codex
// provider. The active account is determined by reading the token stored
// under the bare provider ID.
func (a *App) handleCodexAccountsList(req contracts.CodexAccountsListRequest) (any, *contracts.RPCError) {
	if _, err := a.Providers.Get(req.ProviderID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	prefix := accountKeyPrefix(req.ProviderID)
	ids, err := a.Credentials.ListByPrefix(prefix)
	if err != nil {
		return nil, rpcInternal(err)
	}
	// Determine active account
	var activeAccountID string
	if activeJSON, has, _ := a.Credentials.Get(req.ProviderID); has {
		var active CodexTokenJSON
		if json.Unmarshal([]byte(activeJSON), &active) == nil {
			activeAccountID = active.AccountID
		}
	}
	var accounts []contracts.CodexAccountDTO
	for _, id := range ids {
		// Extract accountID from the key: "{providerID}:account:{accountID}"
		accountID := strings.TrimPrefix(id, prefix)
		tokenJSON, has, _ := a.Credentials.Get(id)
		if !has {
			continue
		}
		var tok CodexTokenJSON
		var expiresAt int64
		if json.Unmarshal([]byte(tokenJSON), &tok) == nil {
			expiresAt = tok.ExpiresAt
		}
		email, name := tok.Email, tok.Name
		// Enrich old tokens that lack email/name by decoding the JWT.
		if email == "" && a.CodexOAuth != nil {
			email, name = a.CodexOAuth.ExtractProfile(tokenJSON)
			// Persist the enriched profile so future reads skip decoding.
			if email != "" {
				tok.Email = email
				tok.Name = name
				if updated, err := json.Marshal(tok); err == nil {
					_ = a.Credentials.Set(id, string(updated))
				}
			}
		}
		accounts = append(accounts, contracts.CodexAccountDTO{
			AccountID: accountID,
			Email:     email,
			Name:      name,
			Active:    accountID == activeAccountID,
			ExpiresAt: expiresAt,
		})
	}
	if accounts == nil {
		accounts = []contracts.CodexAccountDTO{}
	}
	return contracts.CodexAccountsListResult{Accounts: accounts}, nil
}

// handleCodexAccountsSwitch sets a different account as the active one by
// copying its token to the bare provider ID key.
func (a *App) handleCodexAccountsSwitch(req contracts.CodexAccountsSwitchRequest) (any, *contracts.RPCError) {
	if _, err := a.Providers.Get(req.ProviderID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	tokenJSON, has, err := a.Credentials.Get(accountKey(req.ProviderID, req.AccountID))
	if err != nil {
		return nil, rpcInternal(err)
	}
	if !has {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "account not found"}
	}
	if err := a.Credentials.Set(req.ProviderID, tokenJSON); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "ai", "codex switched to account %s", req.AccountID)
	return map[string]bool{"ok": true}, nil
}

// handleCodexRuntimeStatus reports the managed Codex binary state.
func (a *App) handleCodexRuntimeStatus() (any, *contracts.RPCError) {
	if a.CodexRuntime == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex runtime is not configured"}
	}
	status := a.CodexRuntime.Status()
	return contracts.CodexRuntimeStatusResult{
		Installed:     status.Installed,
		Version:       status.Version,
		Path:          status.Path,
		Downloading:   status.Downloading,
		DownloadError: status.DownloadError,
	}, nil
}

// handleCodexRuntimeDownload triggers a download of the Codex binary.
func (a *App) handleCodexRuntimeDownload(req contracts.CodexRuntimeDownloadRequest) (any, *contracts.RPCError) {
	if a.CodexRuntime == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex runtime is not configured"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	binPath, err := a.CodexRuntime.EnsureBinary(ctx, req.Force)
	if err != nil {
		a.log("warn", "ai", "codex runtime download failed: %v", err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	// Read version from status
	status := a.CodexRuntime.Status()
	a.log("info", "ai", "codex runtime downloaded: %s (version %s)", binPath, status.Version)
	return contracts.CodexRuntimeDownloadResult{
		Version: status.Version,
		Path:    binPath,
	}, nil
}

// handleCodexUsage fetches the ChatGPT rate-limit usage for the active
// account of a Codex provider. Returns plan type, used/remaining percent,
// and reset time for the primary (session) and secondary (weekly) windows.
func (a *App) handleCodexUsage(req contracts.CodexUsageRequest) (any, *contracts.RPCError) {
	if a.CodexUsage == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex usage is not configured"}
	}
	tokenJSON, has, err := a.Credentials.Get(req.ProviderID)
	if err != nil {
		return nil, rpcInternal(err)
	}
	if !has {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "no active account — sign in first"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	usage, err := a.CodexUsage.FetchUsage(ctx, tokenJSON)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	result := contracts.CodexUsageResult{
		Plan:                  usage.Plan,
		LimitReached:          usage.LimitReached,
		ResetCreditsAvailable: usage.ResetCreditsAvailable,
	}
	if usage.PrimaryWindow != nil {
		result.PrimaryWindow = &contracts.CodexUsageWindowDTO{
			UsedPercent:       usage.PrimaryWindow.UsedPercent,
			RemainingPercent:  100 - usage.PrimaryWindow.UsedPercent,
			ResetAt:           usage.PrimaryWindow.ResetAt,
			ResetAfterSeconds: usage.PrimaryWindow.ResetAfterSeconds,
		}
	}
	if usage.WeeklyWindow != nil {
		result.WeeklyWindow = &contracts.CodexUsageWindowDTO{
			UsedPercent:       usage.WeeklyWindow.UsedPercent,
			RemainingPercent:  100 - usage.WeeklyWindow.UsedPercent,
			ResetAt:           usage.WeeklyWindow.ResetAt,
			ResetAfterSeconds: usage.WeeklyWindow.ResetAfterSeconds,
		}
	}
	return result, nil
}
