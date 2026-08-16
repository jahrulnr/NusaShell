package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
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

// PersistCodexToken writes token JSON under both the active provider key
// and the account-scoped key used by multi-account routing. Account-scoped
// persistence is skipped when accountID is empty.
func PersistCodexToken(creds CredentialStore, providerID, accountID, tokenJSON string) error {
	if err := creds.Set(providerID, tokenJSON); err != nil {
		return err
	}
	if accountID == "" {
		return nil
	}
	return creds.Set(accountKey(providerID, accountID), tokenJSON)
}

// listCodexAccountIDs returns all stored account IDs for a Codex provider,
// extracted from CredentialStore keys matching "{providerID}:account:*".
func (a *App) listCodexAccountIDs(providerID string) []string {
	prefix := accountKeyPrefix(providerID)
	ids, err := a.Credentials.ListByPrefix(prefix)
	if err != nil {
		return nil
	}
	var accounts []string
	for _, id := range ids {
		accountID := strings.TrimPrefix(id, prefix)
		if accountID != "" {
			accounts = append(accounts, accountID)
		}
	}
	return accounts
}

// isCodexAccountCircuitOpen reports whether an account's circuit breaker
// is currently open (usage exhausted). Returns false if the router is
// not configured or the circuit is closed.
func (a *App) isCodexAccountCircuitOpen(accountID string) bool {
	if a.CodexRouter == nil || accountID == "" {
		return false
	}
	return !a.CodexRouter.CircuitOpenUntil(accountID).IsZero()
}

// codexAccountCircuitUntil returns the unix timestamp at which the
// account's circuit breaker will close, or 0 if the circuit is closed.
func (a *App) codexAccountCircuitUntil(accountID string) int64 {
	if a.CodexRouter == nil || accountID == "" {
		return 0
	}
	until := a.CodexRouter.CircuitOpenUntil(accountID)
	if until.IsZero() {
		return 0
	}
	return until.Unix()
}

// markProviderHasCreds updates the provider's HasAPIKey flag and persists
// it. Errors are logged but not returned — the credential is already
// stored, so a failed flag update is a cosmetic issue, not data loss.
func (a *App) markProviderHasCreds(providerID string, hasKey bool) {
	p, err := a.Providers.Get(providerID)
	if err != nil {
		a.log("warn", "ai", "failed to get provider %s for cred flag update: %v", providerID, err)
		return
	}
	p.HasAPIKey = hasKey
	p.UpdatedAt = time.Now().UTC()
	if err := a.Providers.Save(p); err != nil {
		a.log("warn", "ai", "failed to save provider %s cred flag: %v", providerID, err)
	}
}

// loadCodexToken reads and parses a stored Codex OAuth token for a
// specific account. Returns the parsed token, the raw JSON string, and
// an error if the token is missing or corrupted. Old tokens lacking
// email/name are enriched via JWT decoding and persisted.
func (a *App) loadCodexToken(providerID, accountID string) (CodexTokenJSON, string, error) {
	tokenJSON, has, err := a.Credentials.Get(accountKey(providerID, accountID))
	if err != nil {
		return CodexTokenJSON{}, "", err
	}
	if !has {
		return CodexTokenJSON{}, "", fmt.Errorf("account %s not found", accountID)
	}
	var tok CodexTokenJSON
	if err := json.Unmarshal([]byte(tokenJSON), &tok); err != nil {
		return CodexTokenJSON{}, tokenJSON, fmt.Errorf("invalid token JSON for account %s: %w", accountID, err)
	}
	// Enrich old tokens that lack email/name by decoding the JWT.
	if tok.Email == "" && a.CodexOAuth != nil {
		email, name := a.CodexOAuth.ExtractProfile(tokenJSON)
		if email != "" {
			tok.Email = email
			tok.Name = name
			if updated, err := json.Marshal(tok); err == nil {
				if err := a.Credentials.Set(accountKey(providerID, accountID), string(updated)); err != nil {
					a.log("warn", "ai", "failed to persist enriched profile for account %s: %v", accountID, err)
				}
			}
		}
	}
	return tok, tokenJSON, nil
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
	if err := PersistCodexToken(a.Credentials, req.ProviderID, tok.AccountID, tokenStr); err != nil {
		return nil, rpcInternal(err)
	}
	// Mark provider as having credentials
	a.markProviderHasCreds(req.ProviderID, true)
	a.log("info", "ai", "codex login successful: account %s", tok.AccountID)
	return contracts.CodexLoginResult{AccountID: tok.AccountID}, nil
}

// handleCodexImport imports a token from the Codex CLI auth.json
// (~/.codex/auth.json) into NusaShell's CredentialStore. If an account
// with the same account_id is already stored, the import is skipped
// (idempotent) and Skipped=true is returned.
func (a *App) handleCodexImport(req contracts.CodexImportRequest) (any, *contracts.RPCError) {
	if _, err := a.Providers.Get(req.ProviderID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if a.CodexCLIAuth == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex CLI auth importer is not configured"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := a.CodexCLIAuth.ImportFromCodexCLI(ctx)
	if err != nil {
		a.log("warn", "ai", "codex import failed: %v", err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	// Skip if account already stored (idempotent — avoid duplicates).
	if tok.AccountID != "" {
		if _, has, _ := a.Credentials.Get(accountKey(req.ProviderID, tok.AccountID)); has {
			a.log("info", "ai", "codex import skipped: account %s already present", tok.AccountID)
			return contracts.CodexImportResult{
				AccountID: tok.AccountID,
				Email:     tok.Email,
				Name:      tok.Name,
				Skipped:   true,
			}, nil
		}
	}
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
	if err := PersistCodexToken(a.Credentials, req.ProviderID, tok.AccountID, tokenStr); err != nil {
		return nil, rpcInternal(err)
	}
	a.markProviderHasCreds(req.ProviderID, true)
	a.log("info", "ai", "codex import successful: account %s", tok.AccountID)
	return contracts.CodexImportResult{
		AccountID: tok.AccountID,
		Email:     tok.Email,
		Name:      tok.Name,
	}, nil
}

// handleCodexLogout removes a stored OAuth token. If accountID is empty,
// the active account is removed.
func (a *App) handleCodexLogout(req contracts.CodexLogoutRequest) (any, *contracts.RPCError) {
	if _, err := a.Providers.Get(req.ProviderID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if req.AccountID != "" {
		if err := a.Credentials.Delete(accountKey(req.ProviderID, req.AccountID)); err != nil {
			a.log("warn", "ai", "failed to delete account credential: %v", err)
		}
		// If this was the active account, clear the active key too
		activeJSON, hasActive, _ := a.Credentials.Get(req.ProviderID)
		if hasActive {
			var active CodexTokenJSON
			if json.Unmarshal([]byte(activeJSON), &active) == nil && active.AccountID == req.AccountID {
				if err := a.Credentials.Delete(req.ProviderID); err != nil {
					a.log("warn", "ai", "failed to delete active credential: %v", err)
				}
				a.markProviderHasCreds(req.ProviderID, false)
			}
		}
	} else {
		if err := a.Credentials.Delete(req.ProviderID); err != nil {
			a.log("warn", "ai", "failed to delete active credential: %v", err)
		}
		a.markProviderHasCreds(req.ProviderID, false)
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
		tok, _, err := a.loadCodexToken(req.ProviderID, accountID)
		if err != nil {
			a.log("warn", "ai", "skipping account %s in list: %v", accountID, err)
			continue
		}
		accounts = append(accounts, contracts.CodexAccountDTO{
			AccountID:        accountID,
			Email:            tok.Email,
			Name:             tok.Name,
			Active:           accountID == activeAccountID,
			ExpiresAt:        tok.ExpiresAt,
			CircuitOpen:      a.isCodexAccountCircuitOpen(accountID),
			CircuitOpenUntil: a.codexAccountCircuitUntil(accountID),
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

// handleCodexRefreshCircuits manually triggers a usage poll for all
// stored Codex accounts and updates circuit breaker state. Called by
// the frontend "Refresh" button in the Codex accounts card.
func (a *App) handleCodexRefreshCircuits() (any, *contracts.RPCError) {
	if a.CodexUsage == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex usage is not configured"}
	}
	if a.CodexRouter == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex router is not configured"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	checked := a.RefreshCircuitBreakers(ctx)
	a.log("info", "ai", "codex circuit refresh: checked %d accounts", checked)
	return map[string]any{"ok": true, "checked": checked}, nil
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

// handleCodexUsage fetches the ChatGPT rate-limit usage for ALL stored
// accounts of a Codex provider. Returns an array of per-account usage
// snapshots so the user can compare and switch to the account with the
// most remaining quota.
func (a *App) handleCodexUsage(req contracts.CodexUsageRequest) (any, *contracts.RPCError) {
	if a.CodexUsage == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "codex usage is not configured"}
	}
	if _, err := a.Providers.Get(req.ProviderID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	// Determine active account
	var activeAccountID string
	if activeJSON, has, _ := a.Credentials.Get(req.ProviderID); has {
		var active CodexTokenJSON
		if json.Unmarshal([]byte(activeJSON), &active) == nil {
			activeAccountID = active.AccountID
		}
	}
	accountIDs := a.listCodexAccountIDs(req.ProviderID)
	if len(accountIDs) == 0 {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "no accounts — sign in first"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var accounts []contracts.CodexAccountUsage
	for _, accID := range accountIDs {
		tok, tokenJSON, err := a.loadCodexToken(req.ProviderID, accID)
		if err != nil {
			accounts = append(accounts, contracts.CodexAccountUsage{
				AccountID: accID,
				Active:    accID == activeAccountID,
				Error:     err.Error(),
			})
			continue
		}
		entry := contracts.CodexAccountUsage{
			AccountID:   accID,
			Email:       tok.Email,
			Name:        tok.Name,
			Active:      accID == activeAccountID,
			CircuitOpen: a.isCodexAccountCircuitOpen(accID),
		}
		usage, err := a.CodexUsage.FetchUsage(ctx, tokenJSON)
		if err != nil {
			entry.Error = err.Error()
			accounts = append(accounts, entry)
			continue
		}
		entry.Plan = usage.Plan
		entry.LimitReached = usage.LimitReached
		entry.ResetCreditsAvailable = usage.ResetCreditsAvailable
		if usage.PrimaryWindow != nil {
			entry.PrimaryWindow = &contracts.CodexUsageWindowDTO{
				UsedPercent:       usage.PrimaryWindow.UsedPercent,
				RemainingPercent:  100 - usage.PrimaryWindow.UsedPercent,
				ResetAt:           usage.PrimaryWindow.ResetAt,
				ResetAfterSeconds: usage.PrimaryWindow.ResetAfterSeconds,
			}
		}
		if usage.WeeklyWindow != nil {
			entry.WeeklyWindow = &contracts.CodexUsageWindowDTO{
				UsedPercent:       usage.WeeklyWindow.UsedPercent,
				RemainingPercent:  100 - usage.WeeklyWindow.UsedPercent,
				ResetAt:           usage.WeeklyWindow.ResetAt,
				ResetAfterSeconds: usage.WeeklyWindow.ResetAfterSeconds,
			}
		}
		accounts = append(accounts, entry)
	}
	return contracts.CodexAccountsUsageResult{Accounts: accounts}, nil
}

// refreshCodexCircuit async-fetches the usage status for a Codex account
// and updates the circuit breaker with the exact reset time from the API.
// Called after a 429 failover to refine the circuit-open duration from
// the provider's usage endpoint (which has precise reset timestamps).
// Runs in a goroutine — errors are logged but not surfaced to the user.
// The parent context is respected so the goroutine exits early if the
// turn is cancelled.
func (a *App) refreshCodexCircuit(parent context.Context, providerID, accountID string) {
	if a.CodexUsage == nil || a.CodexRouter == nil || accountID == "" {
		return
	}
	_, tokenJSON, err := a.loadCodexToken(providerID, accountID)
	if err != nil {
		a.log("warn", "ai", "codex circuit refresh: cannot load token for account %s: %v", accountID, err)
		return
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	usage, err := a.CodexUsage.FetchUsage(ctx, tokenJSON)
	if err != nil {
		a.log("warn", "ai", "codex circuit refresh failed for account %s: %v", accountID, err)
		return
	}
	a.applyUsageToCircuit(accountID, usage)
}

// applyUsageToCircuit opens or closes the circuit breaker for an account
// based on the usage result. If LimitReached, opens the circuit until the
// primary window reset time. Otherwise closes it (e.g. user upgraded to
// Plus/Pro and usage was refilled).
func (a *App) applyUsageToCircuit(accountID string, usage CodexUsageResult) {
	if usage.LimitReached {
		if usage.PrimaryWindow != nil && usage.PrimaryWindow.ResetAt > 0 {
			resetAt := time.Unix(usage.PrimaryWindow.ResetAt, 0)
			a.CodexRouter.MarkCircuitOpen(accountID, resetAt)
			a.log("info", "ai", "codex circuit open until %s for account %s (usage limit reached)",
				resetAt.Format(time.RFC3339), accountID)
		}
		return
	}
	if a.CodexRouter.CircuitOpenUntil(accountID).IsZero() {
		return
	}
	a.CodexRouter.ResetCircuit(accountID)
	a.log("info", "ai", "codex circuit closed for account %s (usage limit cleared)", accountID)
}

// RefreshCircuitBreakers polls usage for all stored Codex accounts across
// all Codex providers and updates circuit breaker state. Returns the
// number of accounts checked. Used by the background monitor and the
// manual "Refresh" RPC from the frontend.
func (a *App) RefreshCircuitBreakers(ctx context.Context) int {
	if a.CodexUsage == nil || a.CodexRouter == nil {
		return 0
	}
	checked := 0
	for _, p := range a.Providers.List() {
		if p.Kind != domain.ProviderCodex || !p.Enabled {
			continue
		}
		accountIDs := a.listCodexAccountIDs(p.ID)
		for _, accID := range accountIDs {
			_, tokenJSON, err := a.loadCodexToken(p.ID, accID)
			if err != nil {
				a.log("warn", "ai", "codex circuit poll: cannot load token for account %s: %v", accID, err)
				continue
			}
			checked++
			usage, err := a.CodexUsage.FetchUsage(ctx, tokenJSON)
			if err != nil {
				a.log("warn", "ai", "codex circuit poll failed for account %s: %v", accID, err)
				continue
			}
			a.applyUsageToCircuit(accID, usage)
		}
	}
	return checked
}

// StartCodexCircuitMonitor launches a background goroutine that polls
// Codex usage every 5 minutes and updates circuit breaker state. This
// catches cases where an account's usage quota is refilled (e.g. user
// upgraded to Plus/Pro) without waiting for a 429 failover. The
// goroutine exits when ctx is cancelled.
func (a *App) StartCodexCircuitMonitor(ctx context.Context) {
	if a.CodexUsage == nil || a.CodexRouter == nil {
		return
	}
	a.goSafe("codex", func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		// Initial check on startup so circuits reflect current usage state.
		a.RefreshCircuitBreakers(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				a.RefreshCircuitBreakers(pollCtx)
				cancel()
			}
		}
	})
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
		// Codex models come from the app-server, not a /models endpoint.
		// Skip it — Codex model import is handled separately.
		if p.Kind == domain.ProviderCodex {
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
