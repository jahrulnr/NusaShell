package provider

import (
	"fmt"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type announcementProviderStore struct {
	items map[string]*domain.Provider
}

func (s *announcementProviderStore) List() []*domain.Provider {
	out := make([]*domain.Provider, 0, len(s.items))
	for _, provider := range s.items {
		out = append(out, provider)
	}
	return out
}

func (s *announcementProviderStore) Get(id string) (*domain.Provider, error) {
	provider := s.items[id]
	if provider == nil {
		return nil, fmt.Errorf("provider %q not found", id)
	}
	return provider, nil
}

func (s *announcementProviderStore) Save(provider *domain.Provider) error {
	s.items[provider.ID] = provider
	return nil
}

func (s *announcementProviderStore) Delete(id string) error {
	delete(s.items, id)
	return nil
}

type announcementProviderCredentials struct{}

func (announcementProviderCredentials) Get(string) (string, bool, error) { return "", false, nil }
func (announcementProviderCredentials) Set(string, string) error         { return nil }
func (announcementProviderCredentials) Delete(string) error              { return nil }

func TestProviderConfigChangeReportsConcreteStatus(t *testing.T) {
	store := &announcementProviderStore{items: map[string]*domain.Provider{
		"p1": {ID: "p1", Kind: domain.ProviderChat, Name: "Gateway", BaseURL: "https://old.example", Enabled: true},
	}}
	var gotName, gotAction string
	svc := New(Deps{
		Store:       store,
		Credentials: announcementProviderCredentials{},
		OnConfigChanged: func(name, action string) {
			gotName, gotAction = name, action
		},
	})
	if _, rpcErr := svc.HandleSave(contracts.ProviderSaveRequest{
		ID: "p1", Kind: "chat", Name: "Gateway", BaseURL: "https://new.example", Enabled: false,
	}); rpcErr != nil {
		t.Fatalf("HandleSave: %v", rpcErr)
	}
	if gotName != "Gateway" || gotAction != "disabled" {
		t.Fatalf("save change = %q/%q, want Gateway/disabled", gotName, gotAction)
	}
}

func TestProviderConfigChangeReportsDeletion(t *testing.T) {
	store := &announcementProviderStore{items: map[string]*domain.Provider{
		"p1": {ID: "p1", Kind: domain.ProviderChat, Name: "Gateway", BaseURL: "https://example.com", Enabled: true},
	}}
	var gotName, gotAction string
	svc := New(Deps{
		Store:       store,
		Credentials: announcementProviderCredentials{},
		OnConfigChanged: func(name, action string) {
			gotName, gotAction = name, action
		},
	})
	if _, rpcErr := svc.HandleDelete(contracts.ProviderIDRequest{ID: "p1"}); rpcErr != nil {
		t.Fatalf("HandleDelete: %v", rpcErr)
	}
	if gotName != "Gateway" || gotAction != "deleted" {
		t.Fatalf("delete change = %q/%q, want Gateway/deleted", gotName, gotAction)
	}
}
