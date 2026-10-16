package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/pkg/httpclient"
)

// deviceAuthClient follows the official Codex device authorization protocol.
// Each poll is one bounded request; the caller owns interval and expiry.
type deviceAuthClient struct {
	issuer string
	client *http.Client
}

func (c deviceAuthClient) post(ctx context.Context, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.issuer+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.client.Do(req)
}

func (c deviceAuthClient) start(ctx context.Context) (application.CodexDeviceCode, error) {
	body, _ := json.Marshal(map[string]string{"client_id": oauthClientID})
	resp, err := c.post(ctx, "/api/accounts/deviceauth/usercode", "application/json", bytes.NewReader(body))
	if err != nil {
		return application.CodexDeviceCode{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return application.CodexDeviceCode{}, deviceAuthError(resp)
	}
	var data struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Usercode     string          `json:"usercode"`
		Interval     json.RawMessage `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return application.CodexDeviceCode{}, err
	}
	if data.UserCode == "" {
		data.UserCode = data.Usercode
	}
	if data.DeviceAuthID == "" || data.UserCode == "" {
		return application.CodexDeviceCode{}, fmt.Errorf("device login returned an incomplete code")
	}
	interval := 5
	if len(data.Interval) > 0 {
		n, err := strconv.Atoi(strings.Trim(string(data.Interval), "\" "))
		if err != nil || n < 1 {
			return application.CodexDeviceCode{}, fmt.Errorf("invalid device login polling interval")
		}
		interval = n
	}
	return application.CodexDeviceCode{DeviceAuthID: data.DeviceAuthID, UserCode: data.UserCode, VerificationURL: c.issuer + "/codex/device", IntervalSeconds: interval, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}, nil
}

func deviceAuthError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("Codex device login returned %s: %s", resp.Status, body)
}

func (c deviceAuthClient) poll(ctx context.Context, id, code string) (*TokenJSON, error) {
	body, _ := json.Marshal(map[string]string{"device_auth_id": id, "user_code": code})
	resp, err := c.post(ctx, "/api/accounts/deviceauth/token", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, deviceAuthError(resp)
	}
	var grant struct {
		Code     string `json:"authorization_code"`
		Verifier string `json:"code_verifier"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&grant); err != nil {
		return nil, err
	}
	if grant.Code == "" || grant.Verifier == "" {
		return nil, fmt.Errorf("device login returned an incomplete authorization")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {oauthClientID}, "code": {grant.Code}, "code_verifier": {grant.Verifier}, "redirect_uri": {c.issuer + "/deviceauth/callback"}}
	tokens, err := c.post(ctx, "/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	defer tokens.Body.Close()
	if tokens.StatusCode != http.StatusOK {
		return nil, deviceAuthError(tokens)
	}
	var result struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(tokens.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("device login returned no access token")
	}
	claims := extractJWTClaims(result.IDToken)
	return &TokenJSON{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, AccountID: claims.AccountID, Email: claims.Email, Name: claims.Name, ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
}

func (o *OAuthAdapter) StartDeviceLogin(ctx context.Context) (application.CodexDeviceCode, error) {
	return (deviceAuthClient{issuer: oauthIssuer, client: httpclient.Shared()}).start(ctx)
}
func (o *OAuthAdapter) PollDeviceLogin(ctx context.Context, id, code string) (*application.CodexToken, error) {
	token, err := (deviceAuthClient{issuer: oauthIssuer, client: httpclient.Shared()}).poll(ctx, id, code)
	if err != nil || token == nil {
		return nil, err
	}
	return &application.CodexToken{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, AccountID: token.AccountID, Email: token.Email, Name: token.Name, ExpiresAt: token.ExpiresAt}, nil
}
