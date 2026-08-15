package ai

import (
	"testing"

	"nusashell/domain"
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
