package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultSearchTimeout = 60 * time.Second
	maxSearchBodyBytes   = 4 << 20
	maxSearchErrorBytes  = 2048
)

// SearchConfig configures the Codex standalone web-search endpoint. Token
// refresh and account selection remain outside this wire client; callers pass
// the access token and the account selected for the active turn.
type SearchConfig struct {
	APIKey         string
	BaseURL        string
	HTTPClient     HTTPClient
	Headers        map[string]string
	AccountID      string
	InstallationID string
	SessionID      string
	Timeout        time.Duration
}

// SearchClient calls the Codex standalone web-search endpoint.
type SearchClient struct {
	cfg SearchConfig
}

// SearchRequest is the query-only subset of the Codex standalone search wire
// contract used by NusaShell's web_search tool.
type SearchRequest struct {
	ID        string          `json:"id"`
	AccountID string          `json:"-"`
	Model     string          `json:"model"`
	Commands  *SearchCommands `json:"commands,omitempty"`
}

// SearchCommands contains the standalone search operations supported by this
// initial integration.
type SearchCommands struct {
	SearchQuery []SearchQuery `json:"search_query,omitempty"`
}

// SearchQuery describes one web query. Filtering options are intentionally
// left out until the public web_search contract exposes them.
type SearchQuery struct {
	Q string `json:"q"`
}

// SearchResponse is the provider response. Results remain limited to the
// forward-compatible fields needed to normalize text search results.
type SearchResponse struct {
	Output  string         `json:"output"`
	Results []SearchResult `json:"results,omitempty"`
}

// SearchResult is one opaque-provider result that can be shown as a regular
// NusaShell web_search result when its type is text_result.
type SearchResult struct {
	Type    string `json:"type"`
	RefID   string `json:"ref_id,omitempty"`
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// NewSearchClient validates configuration and creates a standalone search
// client. The HTTP client may carry the Codex cookie jar from the provider
// factory.
func NewSearchClient(cfg SearchConfig) (*SearchClient, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("codex search: access token is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultSearchTimeout
	}
	return &SearchClient{cfg: cfg}, nil
}

// Search executes one bounded standalone Codex search request.
func (c *SearchClient) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	if c == nil {
		return SearchResponse{}, fmt.Errorf("codex search: client is nil")
	}
	if strings.TrimSpace(request.ID) == "" {
		return SearchResponse{}, fmt.Errorf("codex search: request id is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return SearchResponse{}, fmt.Errorf("codex search: model is required")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("codex search: encode request: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.searchURL(), bytes.NewReader(body))
	if err != nil {
		return SearchResponse{}, fmt.Errorf("codex search: create request: %w", err)
	}
	c.setHeaders(httpRequest, request.ID, request.AccountID)
	response, err := c.cfg.HTTPClient.Do(httpRequest)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("codex search: request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, maxSearchErrorBytes))
		if readErr != nil {
			return SearchResponse{}, fmt.Errorf("codex search: HTTP %d (read error: %w)", response.StatusCode, readErr)
		}
		message := strings.TrimSpace(string(data))
		if c.cfg.APIKey != "" {
			message = strings.ReplaceAll(message, c.cfg.APIKey, "[redacted]")
		}
		return SearchResponse{}, fmt.Errorf("codex search: HTTP %d: %s", response.StatusCode, message)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSearchBodyBytes))
	if err != nil {
		return SearchResponse{}, fmt.Errorf("codex search: read response: %w", err)
	}
	var decoded SearchResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return SearchResponse{}, fmt.Errorf("codex search: decode response: %w", err)
	}
	return decoded, nil
}

func (c *SearchClient) searchURL() string {
	base := strings.TrimRight(c.cfg.BaseURL, "/")
	base = strings.TrimSuffix(base, "/responses")
	if strings.HasSuffix(base, "/alpha/search") {
		return base
	}
	return base + "/alpha/search"
}

func (c *SearchClient) setHeaders(request *http.Request, requestID, accountID string) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	request.Header.Set("originator", DefaultOriginator)
	request.Header.Set("User-Agent", CodexUserAgent)
	if accountID == "" {
		accountID = c.cfg.AccountID
	}
	if accountID != "" {
		request.Header.Set("ChatGPT-Account-ID", accountID)
	}
	if c.cfg.InstallationID != "" {
		request.Header.Set(InstallationIDHeader, c.cfg.InstallationID)
	}
	sessionID := c.cfg.SessionID
	if sessionID == "" {
		sessionID = requestID
	}
	if sessionID != "" {
		request.Header.Set("session-id", sessionID)
		request.Header.Set("thread-id", sessionID)
		request.Header.Set("x-client-request-id", sessionID)
	}
	for key, value := range c.cfg.Headers {
		request.Header.Set(key, value)
	}
}
