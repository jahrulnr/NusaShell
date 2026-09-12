package application

import (
	"sort"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

// learnerConvStore is a minimal ConversationStore whose List() returns
// conversations sorted newest-first by UpdatedAt, matching the production
// jsonstore ordering the learner cascade depends on.
type learnerConvStore struct {
	convs map[string]*domain.Conversation
	list  []*domain.Conversation
}

func (s *learnerConvStore) List() []*domain.Conversation {
	if s.list != nil {
		return s.list
	}
	out := make([]*domain.Conversation, 0, len(s.convs))
	for _, c := range s.convs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}
func (s *learnerConvStore) Get(id string) (*domain.Conversation, error) {
	c, ok := s.convs[id]
	if !ok {
		return nil, errNotFound
	}
	return c, nil
}
func (s *learnerConvStore) Save(c *domain.Conversation) error {
	if s.convs == nil {
		s.convs = map[string]*domain.Conversation{}
	}
	s.convs[c.ID] = c
	return nil
}
func (s *learnerConvStore) Delete(id string) error { delete(s.convs, id); return nil }
func (s *learnerConvStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}
func (s *learnerConvStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, nil
}

// learnerProviderStore is a minimal ProviderStore for the cascade tests.
type learnerProviderStore struct {
	providers []*domain.Provider
}

func (s *learnerProviderStore) List() []*domain.Provider { return s.providers }
func (s *learnerProviderStore) Get(id string) (*domain.Provider, error) {
	for _, p := range s.providers {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, errNotFound
}
func (s *learnerProviderStore) Save(p *domain.Provider) error { return nil }
func (s *learnerProviderStore) Delete(id string) error        { return nil }

// learnerCredentialStore returns a fixed key for every provider.
type learnerCredentialStore struct {
	keys map[string]string
}

func (s *learnerCredentialStore) Get(providerID string) (string, bool, error) {
	k, ok := s.keys[providerID]
	return k, ok, nil
}
func (s *learnerCredentialStore) Set(providerID, key string) error { return nil }
func (s *learnerCredentialStore) Delete(providerID string) error   { return nil }
func (s *learnerCredentialStore) ListByPrefix(prefix string) ([]string, error) {
	var out []string
	for k := range s.keys {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func testProvider(id, name string, enabled bool, modelIDs ...string) *domain.Provider {
	p := &domain.Provider{ID: id, Name: name, Enabled: enabled}
	for _, mid := range modelIDs {
		p.Models = append(p.Models, domain.Model{ID: mid, DisplayName: mid})
	}
	return p
}

// Shared provider fixtures for the cascade tests.
var (
	learnerProviderA = testProvider("provA", "Provider A", true, "modelA1", "modelA2")
	learnerProviderB = testProvider("provB", "Provider B", true, "modelB1")
	learnerProviderD = testProvider("provD", "Disabled", false, "modelD1")
)

// TestResolveLearnerModelCascade is a table-driven test covering the four
// cascade steps the UI promises: source conversation model → newest
// conversation model → first enabled provider's first model → empty.
func TestResolveLearnerModelCascade(t *testing.T) {
	type tc struct {
		name               string
		sourceConversation *domain.Conversation
		allConversations   []*domain.Conversation
		providers          []*domain.Provider
		credentials        map[string]string
		want               string
	}

	cases := []tc{
		{
			name: "source conversation model wins",
			sourceConversation: &domain.Conversation{
				ID:     "src",
				Model:  "provA:modelA2",
				Status: "idle",
			},
			allConversations: []*domain.Conversation{
				{ID: "src", Model: "provA:modelA2", Status: "idle"},
				{ID: "other", Model: "provB:modelB1", Status: "idle"},
			},
			providers:   []*domain.Provider{learnerProviderA, learnerProviderB},
			credentials: map[string]string{"provA": "keyA", "provB": "keyB"},
			want:        "provA:modelA2",
		},
		{
			name: "source without model falls back to newest conversation",
			sourceConversation: &domain.Conversation{
				ID:     "src",
				Model:  "",
				Status: "idle",
			},
			allConversations: []*domain.Conversation{
				{ID: "src", Model: "", Status: "idle", UpdatedAt: mustParseTime(t, "2026-01-01T00:00:00Z")},
				{ID: "newest", Model: "provB:modelB1", Status: "idle", UpdatedAt: mustParseTime(t, "2026-09-01T00:00:00Z")},
				{ID: "older", Model: "provA:modelA1", Status: "idle", UpdatedAt: mustParseTime(t, "2026-06-01T00:00:00Z")},
			},
			providers:   []*domain.Provider{learnerProviderA, learnerProviderB},
			credentials: map[string]string{"provA": "keyA", "provB": "keyB"},
			want:        "provB:modelB1",
		},
		{
			name: "newest conversation is selected independent of store order",
			sourceConversation: &domain.Conversation{
				ID: "src", Model: "", Status: "idle",
			},
			allConversations: []*domain.Conversation{
				{ID: "src", Model: "", Status: "idle"},
				{ID: "newest", Model: "provB:modelB1", Status: "idle", UpdatedAt: mustParseTime(t, "2026-09-01T00:00:00Z")},
				{ID: "older", Model: "provA:modelA1", Status: "idle", UpdatedAt: mustParseTime(t, "2026-06-01T00:00:00Z")},
			},
			providers:   []*domain.Provider{learnerProviderA, learnerProviderB},
			credentials: map[string]string{"provA": "keyA", "provB": "keyB"},
			want:        "provB:modelB1",
		},
		{
			name: "no usable conversation falls back to first enabled provider",
			sourceConversation: &domain.Conversation{
				ID:     "src",
				Model:  "",
				Status: "idle",
			},
			allConversations: []*domain.Conversation{
				{ID: "src", Model: "", Status: "idle"},
			},
			providers:   []*domain.Provider{learnerProviderD, learnerProviderA, learnerProviderB},
			credentials: map[string]string{"provA": "keyA", "provB": "keyB"},
			want:        "provA:modelA1",
		},
		{
			name: "nothing resolves returns empty",
			sourceConversation: &domain.Conversation{
				ID:     "src",
				Model:  "",
				Status: "idle",
			},
			allConversations: []*domain.Conversation{
				{ID: "src", Model: "", Status: "idle"},
			},
			providers:   []*domain.Provider{learnerProviderD},
			credentials: map[string]string{},
			want:        "",
		},
		{
			name: "disabled provider model in conversation is skipped to next cascade step",
			sourceConversation: &domain.Conversation{
				ID:     "src",
				Model:  "provD:modelD1",
				Status: "idle",
			},
			allConversations: []*domain.Conversation{
				{ID: "src", Model: "provD:modelD1", Status: "idle"},
			},
			providers:   []*domain.Provider{learnerProviderD, learnerProviderA},
			credentials: map[string]string{"provD": "keyD", "provA": "keyA"},
			want:        "provA:modelA1",
		},
		{
			name: "enabled provider without credential is skipped to next cascade step",
			sourceConversation: &domain.Conversation{
				ID: "src", Model: "provA:modelA1", Status: "idle",
			},
			allConversations: []*domain.Conversation{
				{ID: "src", Model: "provA:modelA1", Status: "idle"},
			},
			providers:   []*domain.Provider{learnerProviderA, learnerProviderB},
			credentials: map[string]string{"provB": "keyB"},
			want:        "provB:modelB1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			convs := map[string]*domain.Conversation{}
			for _, conv := range c.allConversations {
				convs[conv.ID] = conv
			}
			store := &learnerConvStore{convs: convs}
			if c.name == "newest conversation is selected independent of store order" {
				store.list = []*domain.Conversation{convs["older"], convs["src"], convs["newest"]}
			}
			app := &App{
				Conversations: store,
				Providers:     &learnerProviderStore{providers: c.providers},
				Credentials:   &learnerCredentialStore{keys: c.credentials},
			}
			got := app.resolveLearnerModel(c.sourceConversation.ID)
			if got != c.want {
				t.Fatalf("resolveLearnerModel(%q) = %q, want %q", c.sourceConversation.ID, got, c.want)
			}
		})
	}
}

// TestResolveLearnerModelIgnoresDelegateModel is the regression guard: even
// when DelegateModel is set in Settings, the learner cascade must never
// return it. The cascade resolves through conversations/providers, not
// through the delegate-model setting.
func TestResolveLearnerModelIgnoresDelegateModel(t *testing.T) {
	app := &App{
		Settings: &delegateSettingsStore{settings: domain.Settings{DelegateModel: "delegate:model"}},
		Conversations: &learnerConvStore{convs: map[string]*domain.Conversation{
			"src": {ID: "src", Model: "provA:modelA1", Status: "idle"},
		}},
		Providers:   &learnerProviderStore{providers: []*domain.Provider{learnerProviderA}},
		Credentials: &learnerCredentialStore{keys: map[string]string{"provA": "keyA"}},
	}
	got := app.resolveLearnerModel("src")
	if got == "delegate:model" {
		t.Fatal("resolveLearnerModel returned the delegate model; the learner cascade must not inherit DelegateModel")
	}
	if got != "provA:modelA1" {
		t.Fatalf("resolveLearnerModel = %q, want %q", got, "provA:modelA1")
	}
}
