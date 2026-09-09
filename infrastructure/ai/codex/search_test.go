package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchClientPostsQueryAndParsesResults(t *testing.T) {
	var requestBody SearchRequest
	var gotPath string
	var gotAuthorization, gotAccount, gotInstallation, gotSession string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		gotInstallation = r.Header.Get(InstallationIDHeader)
		gotSession = r.Header.Get("session-id")
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"fresh results","results":[{"type":"text_result","ref_id":"turn0search0","url":"https://example.com/go","title":"Go","snippet":"Go result"},{"type":"image_result","ref_id":"turn0image0"}]}`))
	}))
	defer server.Close()

	client, err := NewSearchClient(SearchConfig{
		APIKey:         "access-token",
		BaseURL:        server.URL + "/codex",
		HTTPClient:     server.Client(),
		AccountID:      "acct-1",
		InstallationID: "install-1",
		SessionID:      "conversation-1",
	})
	if err != nil {
		t.Fatalf("NewSearchClient: %v", err)
	}

	response, err := client.Search(t.Context(), SearchRequest{
		ID:        "conversation-1",
		AccountID: "selected-acct",
		Model:     "gpt-5-codex",
		Commands: &SearchCommands{SearchQuery: []SearchQuery{{
			Q: "latest Go release",
		}}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/codex/alpha/search" {
		t.Fatalf("path = %q, want /codex/alpha/search", gotPath)
	}
	if gotAuthorization != "Bearer access-token" {
		t.Fatalf("Authorization = %q", gotAuthorization)
	}
	if gotAccount != "selected-acct" {
		t.Fatalf("ChatGPT-Account-ID = %q", gotAccount)
	}
	if gotInstallation != "install-1" {
		t.Fatalf("installation id = %q", gotInstallation)
	}
	if gotSession != "conversation-1" {
		t.Fatalf("session id = %q", gotSession)
	}
	if requestBody.ID != "conversation-1" || requestBody.Model != "gpt-5-codex" {
		t.Fatalf("request identity = %+v", requestBody)
	}
	if requestBody.Commands == nil || len(requestBody.Commands.SearchQuery) != 1 {
		t.Fatalf("commands = %+v", requestBody.Commands)
	}
	query := requestBody.Commands.SearchQuery[0]
	if query.Q != "latest Go release" {
		t.Fatalf("query = %+v", query)
	}
	if response.Output != "fresh results" || len(response.Results) != 2 {
		t.Fatalf("response = %+v", response)
	}
	if response.Results[0].Type != "text_result" || response.Results[0].URL != "https://example.com/go" {
		t.Fatalf("first result = %+v", response.Results[0])
	}
}

func TestSearchClientUsesResponsesBaseURLPrefixOnce(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"ok"}`))
	}))
	defer server.Close()

	client, err := NewSearchClient(SearchConfig{
		APIKey:     "token",
		BaseURL:    server.URL + "/codex/responses",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewSearchClient: %v", err)
	}
	if _, err := client.Search(t.Context(), SearchRequest{ID: "c1", Model: "gpt-5-codex"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/codex/alpha/search" {
		t.Fatalf("path = %q, want /codex/alpha/search", gotPath)
	}
}

func TestSearchClientRejectsHTTPErrorWithoutLeakingLargeBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(strings.Repeat("rate limited token ", 500)))
	}))
	defer server.Close()

	client, err := NewSearchClient(SearchConfig{
		APIKey:     "token",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewSearchClient: %v", err)
	}
	_, err = client.Search(t.Context(), SearchRequest{ID: "c1", Model: "gpt-5-codex"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("error = %v, want HTTP 429", err)
	}
	if len(err.Error()) > 5000 {
		t.Fatalf("error length = %d, want bounded", len(err.Error()))
	}
	if strings.Contains(err.Error(), "token") {
		t.Fatalf("error leaked access token: %v", err)
	}
}
