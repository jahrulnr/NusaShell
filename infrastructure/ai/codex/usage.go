package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"nusashell/application"
)

// whamUsageURL is the ChatGPT rate-limit usage endpoint.
const whamUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// whamUsageResponse is the raw JSON from the wham/usage endpoint.
type whamUsageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Allowed       bool `json:"allowed"`
		LimitReached  bool `json:"limit_reached"`
		PrimaryWindow *struct {
			UsedPercent        int   `json:"used_percent"`
			LimitWindowSeconds int64 `json:"limit_window_seconds"`
			ResetAfterSeconds  int64 `json:"reset_after_seconds"`
			ResetAt            int64 `json:"reset_at"`
		} `json:"primary_window"`
		SecondaryWindow *struct {
			UsedPercent        int   `json:"used_percent"`
			LimitWindowSeconds int64 `json:"limit_window_seconds"`
			ResetAfterSeconds  int64 `json:"reset_after_seconds"`
			ResetAt            int64 `json:"reset_at"`
		} `json:"secondary_window"`
	} `json:"rate_limit"`
	RateLimitResetCredits struct {
		AvailableCount int `json:"available_count"`
	} `json:"rate_limit_reset_credits"`
}

// FetchUsage calls the ChatGPT wham/usage endpoint with the stored OAuth
// token and returns the parsed usage snapshot.
func FetchUsage(ctx context.Context, tokenJSON string) (application.CodexUsageResult, error) {
	tok, err := UnmarshalToken(tokenJSON)
	if err != nil {
		return application.CodexUsageResult{}, fmt.Errorf("codex usage: parse token: %w", err)
	}
	if tok.AccessToken == "" {
		return application.CodexUsageResult{}, fmt.Errorf("codex usage: no access token in stored token")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", whamUsageURL, nil)
	if err != nil {
		return application.CodexUsageResult{}, fmt.Errorf("codex usage: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("originator", "codex_cli_rs")
	if tok.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", tok.AccountID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return application.CodexUsageResult{}, fmt.Errorf("codex usage: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return application.CodexUsageResult{}, fmt.Errorf("codex usage: API returned %s: %s", resp.Status, string(body))
	}

	var raw whamUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return application.CodexUsageResult{}, fmt.Errorf("codex usage: decode response: %w", err)
	}

	result := application.CodexUsageResult{
		Plan:                  raw.PlanType,
		LimitReached:          raw.RateLimit.LimitReached,
		ResetCreditsAvailable: raw.RateLimitResetCredits.AvailableCount,
	}
	if raw.RateLimit.PrimaryWindow != nil {
		result.PrimaryWindow = &application.CodexUsageWindow{
			UsedPercent:       raw.RateLimit.PrimaryWindow.UsedPercent,
			ResetAt:           raw.RateLimit.PrimaryWindow.ResetAt,
			ResetAfterSeconds: raw.RateLimit.PrimaryWindow.ResetAfterSeconds,
		}
	}
	if raw.RateLimit.SecondaryWindow != nil {
		result.WeeklyWindow = &application.CodexUsageWindow{
			UsedPercent:       raw.RateLimit.SecondaryWindow.UsedPercent,
			ResetAt:           raw.RateLimit.SecondaryWindow.ResetAt,
			ResetAfterSeconds: raw.RateLimit.SecondaryWindow.ResetAfterSeconds,
		}
	}

	return result, nil
}
