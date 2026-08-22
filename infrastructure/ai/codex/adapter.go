// Package codex provides the infrastructure adapter that bridges the
// application.CodexRuntime and application.CodexOAuth ports to the
// concrete runtime.Manager and codex.Login implementations.
package codex

import (
	"context"
	"sync"

	"nusashell/application"
	"nusashell/infrastructure/ai/codex/runtime"
	"nusashell/infrastructure/config"
)

// RuntimeAdapter implements application.CodexRuntime using the
// infrastructure/ai/codex/runtime.Manager.
type RuntimeAdapter struct {
	mgr *runtime.Manager

	// downloadMu ensures only one download runs at a time.
	downloadMu sync.Mutex
	// downloading tracks in-flight download state for Status() reporting.
	downloading   bool
	downloadError string
}

// NewRuntimeAdapter creates a CodexRuntime backed by runtime.Manager.
func NewRuntimeAdapter() (*RuntimeAdapter, error) {
	mgr, err := runtime.NewManager()
	if err != nil {
		return nil, err
	}
	return &RuntimeAdapter{mgr: mgr}, nil
}

func (r *RuntimeAdapter) Status() application.CodexRuntimeStatus {
	man, err := r.mgr.LoadManifest()
	if err != nil {
		return application.CodexRuntimeStatus{}
	}
	r.downloadMu.Lock()
	downloading := r.downloading
	dlErr := r.downloadError
	r.downloadMu.Unlock()
	if man.ActiveVersion == "" {
		return application.CodexRuntimeStatus{
			Downloading:   downloading,
			DownloadError: dlErr,
		}
	}
	return application.CodexRuntimeStatus{
		Installed:     true,
		Version:       man.ActiveVersion,
		Path:          r.mgr.BinaryPath(man.ActiveVersion),
		Downloading:   downloading,
		DownloadError: dlErr,
	}
}

func (r *RuntimeAdapter) EnsureBinary(ctx context.Context, force bool) (string, error) {
	r.downloadMu.Lock()
	r.downloading = true
	r.downloadError = ""
	r.downloadMu.Unlock()
	defer func() {
		r.downloadMu.Lock()
		r.downloading = false
		r.downloadMu.Unlock()
	}()
	binPath, err := r.mgr.EnsureBinary(ctx)
	if err != nil {
		r.downloadMu.Lock()
		r.downloadError = err.Error()
		r.downloadMu.Unlock()
		return "", err
	}
	if force {
		// For force re-download, delete the manifest and re-download
		man, _ := r.mgr.LoadManifest()
		if man.ActiveVersion != "" {
			// The runtime manager doesn't have a "remove" method, so
			// we just re-download. In practice, EnsureBinary returns
			// the cached binary. A true force re-download would need
			// a Delete method on the manager. For now, force is a no-op
			// if the binary is already installed.
		}
	}
	return binPath, nil
}

// OAuthAdapter implements application.CodexOAuth using the
// infrastructure/ai/codex.Login function.
type OAuthAdapter struct{}

func NewOAuthAdapter() *OAuthAdapter { return &OAuthAdapter{} }

func (o *OAuthAdapter) Login(ctx context.Context) (application.CodexToken, error) {
	tok, err := Login(ctx, LoginOptions{OpenBrowser: true})
	if err != nil {
		return application.CodexToken{}, err
	}
	return application.CodexToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		AccountID:    tok.AccountID,
		Email:        tok.Email,
		Name:         tok.Name,
		ExpiresAt:    tok.ExpiresAt,
	}, nil
}

// ExtractProfile decodes a stored token JSON and returns email + name.
// If the token already has email/name fields, they are returned directly.
// Otherwise the JWT access_token is decoded to extract the profile claims.
func (o *OAuthAdapter) ExtractProfile(tokenJSON string) (string, string) {
	tok, err := UnmarshalToken(tokenJSON)
	if err != nil {
		return "", ""
	}
	if tok.Email != "" {
		return tok.Email, tok.Name
	}
	claims := extractJWTClaims(tok.AccessToken)
	return claims.Email, claims.Name
}

// UsageAdapter implements application.CodexUsage by calling FetchUsage.
type UsageAdapter struct{}

func NewUsageAdapter() *UsageAdapter { return &UsageAdapter{} }

func (u *UsageAdapter) FetchUsage(ctx context.Context, tokenJSON string) (application.CodexUsageResult, error) {
	return FetchUsage(ctx, tokenJSON)
}

// CLIAuthImporterAdapter implements application.CodexCLIAuthImporter by
// reading the Codex CLI auth.json from the user's home directory.
type CLIAuthImporterAdapter struct{}

func NewCLIAuthImporterAdapter() *CLIAuthImporterAdapter { return &CLIAuthImporterAdapter{} }

func (c *CLIAuthImporterAdapter) ImportFromCodexCLI(ctx context.Context) (application.CodexToken, error) {
	tok, err := ReadCodexCLIAuth(config.CodexCLIAuthPath())
	if err != nil {
		return application.CodexToken{}, err
	}
	return application.CodexToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		AccountID:    tok.AccountID,
		Email:        tok.Email,
		Name:         tok.Name,
		ExpiresAt:    tok.ExpiresAt,
	}, nil
}

// ContextWindowCacheAdapter implements application.CodexContextWindowCache
// by reading the Codex CLI's local model cache (~/.codex/models_cache.json).
// It returns the real context window the Codex app-server enforces, which
// is often smaller than the stale value stored in providers.json.
type ContextWindowCacheAdapter struct{}

func NewContextWindowCacheAdapter() *ContextWindowCacheAdapter {
	return &ContextWindowCacheAdapter{}
}

func (c *ContextWindowCacheAdapter) ContextWindow(modelID string) (int, bool) {
	cache := loadCodexModelsCache()
	if cache == nil {
		return 0, false
	}
	cw, ok := cache[modelID]
	return cw, ok
}
