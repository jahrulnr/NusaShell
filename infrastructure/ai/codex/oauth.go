// OAuth flow for the Codex backend. Ported from the genai example
// txt_to_txt_oauth2_codex/main.go (Apache 2.0, Marc-Antoine Ruel).
//
// Implements PKCE Authorization Code flow against auth.openai.com using
// the same client_id, scopes, and callback port as the official Codex CLI.
package codex

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OAuth constants — match the official Codex CLI.
const (
	oauthIssuer   = "https://auth.openai.com"
	oauthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	oauthScopes   = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	callbackPort  = 1455
	callbackPath  = "/auth/callback"
)

// TokenJSON is the on-disk format for cached OAuth tokens. Stored in
// NusaShell's CredentialStore as a JSON string.
type TokenJSON struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Name         string `json:"name,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // unix seconds, 0 = unknown
}

// LoginOptions configures the OAuth login flow.
type LoginOptions struct {
	// Originator identifies the client type. Defaults to DefaultOriginator.
	Originator string
	// OpenBrowser controls whether to automatically open the browser.
	// If false, the authorize URL is printed to stderr.
	OpenBrowser bool
}

// Login runs the full PKCE Authorization Code flow and returns the
// obtained tokens. Ported from genai example doBrowserLogin.
func Login(ctx context.Context, opts LoginOptions) (*TokenJSON, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}
	originator := opts.Originator
	if originator == "" {
		originator = DefaultOriginator
	}
	redirectURI, code, err := waitForCallback(ctx, oauthIssuer+"/oauth/authorize", url.Values{
		"response_type":              {"code"},
		"client_id":                  {oauthClientID},
		"scope":                      {oauthScopes},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"codex_cli_simplified_flow":  {"true"},
		"id_token_add_organizations": {"true"},
		"originator":                 {originator},
	}, opts.OpenBrowser)
	if err != nil {
		return nil, err
	}
	tokens, err := exchangeCode(ctx, redirectURI, verifier, code)
	if err != nil {
		return nil, err
	}
	claims := extractJWTClaims(tokens.IDToken)
	return &TokenJSON{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		AccountID:    claims.AccountID,
		Email:        claims.Email,
		Name:         claims.Name,
		ExpiresAt:    time.Now().Add(time.Hour).Unix(), // access tokens last ~1h
	}, nil
}

// Refresh uses a refresh_token to obtain a new access_token. Ported from
// genai example refreshToken.
func Refresh(ctx context.Context, old *TokenJSON) (*TokenJSON, error) {
	body, err := json.Marshal(map[string]string{
		"client_id":     oauthClientID,
		"grant_type":    "refresh_token",
		"refresh_token": old.RefreshToken,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", oauthIssuer+"/oauth/token", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh returned %s: %s", resp.Status, b)
	}
	var tok struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("decoding refresh response: %w", err)
	}
	result := &TokenJSON{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		AccountID:    old.AccountID,
		Email:        old.Email,
		Name:         old.Name,
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}
	// Keep old refresh token if the server didn't rotate it.
	if result.RefreshToken == "" {
		result.RefreshToken = old.RefreshToken
	}
	// Update claims if a new id_token was returned.
	if tok.IDToken != "" {
		if c := extractJWTClaims(tok.IDToken); c.AccountID != "" {
			result.AccountID = c.AccountID
			if c.Email != "" {
				result.Email = c.Email
			}
			if c.Name != "" {
				result.Name = c.Name
			}
		}
	}
	return result, nil
}

// IsExpired returns true if the access token has expired or will expire
// within the given margin.
func (t *TokenJSON) IsExpired(margin time.Duration) bool {
	if t.ExpiresAt == 0 {
		return false // unknown expiry, assume valid
	}
	return time.Now().Add(margin).Unix() > t.ExpiresAt
}

// Marshal returns the token as a JSON string for storage in CredentialStore.
func (t *TokenJSON) Marshal() (string, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UnmarshalToken parses a JSON string from CredentialStore into a TokenJSON.
func UnmarshalToken(s string) (*TokenJSON, error) {
	var t TokenJSON
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ---- internal OAuth helpers (ported from genai example) ----

type codeExchangeResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func exchangeCode(ctx context.Context, redirectURI, verifier, code string) (*codeExchangeResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {oauthClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", oauthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging code for tokens: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange returned %s: %s", resp.Status, b)
	}
	var tok codeExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	return &tok, nil
}

type callbackResult struct {
	code string
	err  error
}

func waitForCallback(ctx context.Context, authEndpoint string, params url.Values, openBrowser bool) (redirectURI, code string, err error) {
	state, err := randomString(32)
	if err != nil {
		return "", "", fmt.Errorf("generating state: %w", err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", callbackPort))
	if err != nil {
		return "", "", fmt.Errorf("listening on localhost:%d: %w", callbackPort, err)
	}
	defer ln.Close()
	redirectURI = fmt.Sprintf("http://localhost:%d%s", callbackPort, callbackPath)

	ch := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			ch <- callbackResult{err: fmt.Errorf("state mismatch")}
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			ch <- callbackResult{err: fmt.Errorf("authorization error: %s: %s", e, r.URL.Query().Get("error_description"))}
			http.Error(w, "Authorization failed: "+e, http.StatusBadRequest)
			return
		}
		c := r.URL.Query().Get("code")
		if c == "" {
			ch <- callbackResult{err: fmt.Errorf("no code in callback")}
			http.Error(w, "Missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "<html><body><h1>Authorization successful!</h1><p>You can close this tab.</p></body></html>")
		ch <- callbackResult{code: c}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(context.WithoutCancel(ctx))

	params.Set("state", state)
	params.Set("redirect_uri", redirectURI)
	authURL := authEndpoint + "?" + params.Encode()

	fmt.Fprintf(os.Stderr, "Opening browser for OpenAI login...\n")
	fmt.Fprintf(os.Stderr, "If the browser does not open, visit:\n%s\n", authURL)
	if openBrowser {
		if err := openBrowserURL(authURL); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not open browser: %v\n", err)
		}
	}

	var res callbackResult
	select {
	case res = <-ch:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	if res.err != nil {
		return "", "", res.err
	}
	return redirectURI, res.code, nil
}

func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowserURL(u string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", u).Start()
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", strings.ReplaceAll(u, "&", "^&")).Start()
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

// jwtClaims holds the fields NusaShell extracts from a ChatGPT id_token.
type jwtClaims struct {
	AccountID string
	Email     string
	Name      string
}

// extractJWTClaims decodes a JWT (id_token or access_token) and extracts
// the chatgpt_account_id, email, and name. Email/name may be in the standard
// id_token claims or in the https://api.openai.com/profile claim (access_token).
func extractJWTClaims(jwt string) jwtClaims {
	parts := strings.SplitN(jwt, ".", 3)
	if len(parts) < 2 {
		return jwtClaims{}
	}
	// JWT payload is base64url-encoded without padding.
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Auth  struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
		Profile struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"https://api.openai.com/profile"`
	}
	if err := json.Unmarshal(b, &claims); err != nil {
		return jwtClaims{}
	}
	email := claims.Email
	if email == "" {
		email = claims.Profile.Email
	}
	name := claims.Name
	if name == "" {
		name = claims.Profile.Name
	}
	return jwtClaims{
		AccountID: claims.Auth.AccountID,
		Email:     email,
		Name:      name,
	}
}

// extractAccountID decodes a JWT id_token and extracts the
// chatgpt_account_id from the https://api.openai.com/auth claim.
// Kept for backward compatibility with Refresh callers.
func extractAccountID(idToken string) string {
	return extractJWTClaims(idToken).AccountID
}
