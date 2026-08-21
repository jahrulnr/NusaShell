package ai

import (
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/codex"
)

type stubCreds struct {
	val string
	ok  bool
}

func (s *stubCreds) Get(string) (string, bool, error)      { return s.val, s.ok, nil }
func (s *stubCreds) Set(_, v string) error                 { s.val = v; s.ok = true; return nil }
func (s *stubCreds) Delete(string) error                   { s.ok = false; return nil }
func (s *stubCreds) ListByPrefix(string) ([]string, error) { return nil, nil }

func TestNewProviderHTTPClientHasNoBodyTimeout(t *testing.T) {
	client := newProviderHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so SSE bodies can outlive 60s", client.Timeout)
	}
}

func TestNewFactoryCodexNoToken(t *testing.T) {
	f := NewFactory(&stubCreds{})
	_, err := f(nil, &domain.Provider{Kind: domain.ProviderCodex}, "")
	if err != nil {
		t.Fatalf("Codex with no token should not error at factory: %v", err)
	}
}

func TestNewFactoryCodexWithValidToken(t *testing.T) {
	f := NewFactory(&stubCreds{ok: true, val: `{"access_token":"tok","refresh_token":"ref","account_id":"acc","expires_at":9999999999}`})
	_, err := f(nil, &domain.Provider{Kind: domain.ProviderCodex, ID: "codex"}, "")
	if err != nil {
		t.Fatalf("Codex with valid token should not error: %v", err)
	}
}

func TestNewImageGeneratorFactoryRoutesCodex(t *testing.T) {
	f := NewImageGeneratorFactory(&stubCreds{})
	gen, err := f(&domain.Provider{Kind: domain.ProviderCodex, ID: "codex", BaseURL: "https://chatgpt.com/backend-api/codex"}, `{"access_token":"tok","account_id":"acc","expires_at":9999999999}`)
	if err != nil {
		t.Fatal(err)
	}
	client, ok := gen.(*codex.ImagesClient)
	if !ok {
		t.Fatalf("got %#v, want *codex.ImagesClient", gen)
	}
	if client.AccessToken != "tok" || client.AccountID != "acc" {
		t.Fatalf("client = %+v", client)
	}
	if client.BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("base = %q", client.BaseURL)
	}
}

func TestNewImageGeneratorFactoryRejectsMessages(t *testing.T) {
	f := NewImageGeneratorFactory(&stubCreds{})
	_, err := f(&domain.Provider{Kind: domain.ProviderMessages, BaseURL: "https://api.anthropic.com"}, "key")
	if err == nil || !strings.Contains(err.Error(), "no image generation API") {
		t.Fatalf("err = %v", err)
	}
}
