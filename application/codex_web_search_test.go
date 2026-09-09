package application

import (
	"context"
	"testing"

	"nusashell/domain"
)

type codexWebSearchBackendStub struct {
	request CodexSearchRequest
}

func (s *codexWebSearchBackendStub) Search(_ context.Context, request CodexSearchRequest) (CodexSearchResponse, error) {
	s.request = request
	return CodexSearchResponse{Summary: "summary", Results: []CodexSearchResult{{URL: "https://example.com"}}}, nil
}

func TestSearchCodexWebResolvesActiveCodexCredential(t *testing.T) {
	backend := &codexWebSearchBackendStub{}
	factoryCalls := 0
	creds := &memCreds{m: map[string]string{
		"codex":                       "provider-token",
		accountKey("codex", "acct-2"): "account-token",
	}}
	if got, has, _ := creds.Get(accountKey("codex", "acct-2")); !has || got != "account-token" {
		t.Fatalf("account credential = %q, has=%v", got, has)
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{
			"conversation-1": {ID: "conversation-1", ProviderRoute: "acct-2"},
		}},
		Providers:   &fakeProviderStore{items: map[string]*domain.Provider{"codex": {ID: "codex", Kind: domain.ProviderCodex}}},
		Credentials: creds,
		CodexSearchFactory: func(_ context.Context, provider *domain.Provider, apiKey string) (CodexSearchBackend, error) {
			factoryCalls++
			if provider.ID != "codex" || apiKey != "account-token" {
				t.Fatalf("factory arguments = provider=%+v key=%q", provider, apiKey)
			}
			return backend, nil
		},
	}
	if got := app.selectedCodexAccount("conversation-1"); got != "acct-2" {
		t.Fatalf("selected account = %q, want acct-2", got)
	}

	response, err := app.SearchCodexWeb(context.Background(), CodexSearchRequest{
		ProviderID:     "codex",
		ConversationID: "conversation-1",
		Model:          "gpt-5-codex",
		Query:          "latest Go release",
		Limit:          3,
	})
	if err != nil {
		t.Fatalf("SearchCodexWeb: %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}
	if backend.request.AccountID != "acct-2" || backend.request.ConversationID != "conversation-1" || backend.request.Model != "gpt-5-codex" || backend.request.Query != "latest Go release" || backend.request.Limit != 3 {
		t.Fatalf("backend request = %+v", backend.request)
	}
	if response.Summary != "summary" || len(response.Results) != 1 {
		t.Fatalf("response = %+v", response)
	}
}

func TestSearchCodexWebRejectsNonCodexProvider(t *testing.T) {
	factoryCalls := 0
	app := &App{
		Providers:   &fakeProviderStore{items: map[string]*domain.Provider{"openai": {ID: "openai", Kind: domain.ProviderResponses}}},
		Credentials: &memCreds{m: map[string]string{"openai": "access-token"}},
		CodexSearchFactory: func(context.Context, *domain.Provider, string) (CodexSearchBackend, error) {
			factoryCalls++
			return nil, nil
		},
	}
	if _, err := app.SearchCodexWeb(context.Background(), CodexSearchRequest{ProviderID: "openai", Query: "query"}); err == nil {
		t.Fatal("SearchCodexWeb should reject non-Codex provider")
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls)
	}
}

func TestSearchCodexWebRejectsMissingSelectedAccountCredential(t *testing.T) {
	factoryCalls := 0
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{
			"conversation-1": {ID: "conversation-1", ProviderRoute: "acct-missing"},
		}},
		Providers:   &fakeProviderStore{items: map[string]*domain.Provider{"codex": {ID: "codex", Kind: domain.ProviderCodex}}},
		Credentials: &memCreds{m: map[string]string{"codex": "provider-token"}},
		CodexSearchFactory: func(context.Context, *domain.Provider, string) (CodexSearchBackend, error) {
			factoryCalls++
			return nil, nil
		},
	}
	if _, err := app.SearchCodexWeb(context.Background(), CodexSearchRequest{ProviderID: "codex", ConversationID: "conversation-1", Query: "query"}); err == nil {
		t.Fatal("SearchCodexWeb should reject a missing selected account credential")
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls)
	}
}
