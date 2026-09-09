package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"

	"github.com/jahrulnr/searchwire"
)

type codexSearchProviderStore struct {
	provider *domain.Provider
}

func (s codexSearchProviderStore) List() []*domain.Provider { return []*domain.Provider{s.provider} }
func (s codexSearchProviderStore) Get(id string) (*domain.Provider, error) {
	if s.provider != nil && s.provider.ID == id {
		return s.provider, nil
	}
	return nil, errors.New("provider not found")
}

func (codexSearchProviderStore) Save(*domain.Provider) error { return nil }
func (codexSearchProviderStore) Delete(string) error         { return nil }

type codexSearchStub struct {
	response application.CodexSearchResponse
	err      error
	calls    int
}

func (s *codexSearchStub) SearchCodexWeb(context.Context, application.CodexSearchRequest) (application.CodexSearchResponse, error) {
	s.calls++
	return s.response, s.err
}

func TestWebSearchUsesCodexBeforeSearchwireForCodexProvider(t *testing.T) {
	codex := &codexSearchStub{response: application.CodexSearchResponse{
		Summary: "Codex summary",
		Results: []application.CodexSearchResult{{Title: "Codex result", URL: "https://example.com/codex", Snippet: "from Codex"}},
	}}
	searchwireCalls := 0
	tb := &Toolbox{
		Providers:   codexSearchProviderStore{provider: &domain.Provider{ID: "codex", Kind: domain.ProviderCodex}},
		CodexSearch: codex,
		searchwireSearch: func(context.Context, *searchwire.Searcher, string, searchwire.SearchOptions) (*searchwire.Response, error) {
			searchwireCalls++
			return nil, errors.New("searchwire must not run before Codex")
		},
	}

	ctx := application.WithModel(application.WithProviderID(context.Background(), "codex"), "gpt-5-codex")
	output, err := tb.Execute(ctx, "web_search", []byte(`{"query":"codex query","limit":5}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if codex.calls != 1 {
		t.Fatalf("Codex calls = %d, want 1", codex.calls)
	}
	if searchwireCalls != 0 {
		t.Fatalf("searchwire calls = %d, want 0", searchwireCalls)
	}
	if !strings.Contains(output, "Codex result") || !strings.Contains(output, "provider: codex") {
		t.Fatalf("output = %s, want normalized Codex result", output)
	}
}

func TestWebSearchFallsBackToSearchwireWhenCodexFails(t *testing.T) {
	codex := &codexSearchStub{err: errors.New("Codex search unavailable")}
	searchwireCalls := 0
	tb := &Toolbox{
		Providers:   codexSearchProviderStore{provider: &domain.Provider{ID: "codex", Kind: domain.ProviderCodex}},
		CodexSearch: codex,
		searchwireSearch: func(context.Context, *searchwire.Searcher, string, searchwire.SearchOptions) (*searchwire.Response, error) {
			searchwireCalls++
			return &searchwire.Response{Results: []searchwire.Result{{
				Title: "Fallback result", URL: "https://example.com/fallback", Snippet: "from searchwire", Sources: []string{"brave"},
			}}}, nil
		},
	}

	ctx := application.WithModel(application.WithProviderID(context.Background(), "codex"), "gpt-5-codex")
	output, err := tb.Execute(ctx, "web_search", []byte(`{"query":"fallback query"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if codex.calls != 1 || searchwireCalls != 1 {
		t.Fatalf("calls = Codex %d, searchwire %d; want 1/1", codex.calls, searchwireCalls)
	}
	if !strings.Contains(output, "Fallback result") || !strings.Contains(output, "provider: searchwire") {
		t.Fatalf("output = %s, want searchwire fallback result", output)
	}
}

func TestWebSearchUsesSearchwireForNonCodexProvider(t *testing.T) {
	codex := &codexSearchStub{}
	searchwireCalls := 0
	tb := &Toolbox{
		Providers:   codexSearchProviderStore{provider: &domain.Provider{ID: "openai", Kind: domain.ProviderResponses}},
		CodexSearch: codex,
		searchwireSearch: func(context.Context, *searchwire.Searcher, string, searchwire.SearchOptions) (*searchwire.Response, error) {
			searchwireCalls++
			return &searchwire.Response{Results: []searchwire.Result{{
				Title: "OpenAI provider result", URL: "https://example.com/openai", Snippet: "from searchwire", Sources: []string{"wikipedia"},
			}}}, nil
		},
	}

	ctx := application.WithProviderID(context.Background(), "openai")
	output, err := tb.Execute(ctx, "web_search", []byte(`{"query":"ordinary query"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if codex.calls != 0 || searchwireCalls != 1 {
		t.Fatalf("calls = Codex %d, searchwire %d; want 0/1", codex.calls, searchwireCalls)
	}
	if !strings.Contains(output, "OpenAI provider result") {
		t.Fatalf("output = %s, want searchwire result", output)
	}
}

func TestFormatCodexWebSearchBoundsSummaryBeforeResults(t *testing.T) {
	output := formatCodexWebSearch(application.CodexSearchResponse{
		Summary: strings.Repeat("summary ", 1000),
		Results: []application.CodexSearchResult{{Title: "Result", URL: "https://example.com", Snippet: "snippet"}},
	}, 10)
	if !strings.Contains(output, "summary_truncated: true") {
		t.Fatalf("output = %s, want summary_truncated metadata", output[:min(len(output), 500)])
	}
	if !strings.Contains(output, "https://example.com") {
		t.Fatalf("output lost result URL: %s", output[:min(len(output), 500)])
	}
}
