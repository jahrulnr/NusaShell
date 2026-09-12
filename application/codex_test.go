package application

import (
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestPersistCodexTokenWritesActiveAndAccountKeys(t *testing.T) {
	creds := &memCreds{}
	if err := PersistCodexToken(creds, "prov", "acc1", `{"access_token":"fresh"}`); err != nil {
		t.Fatalf("PersistCodexToken: %v", err)
	}
	if got, has, _ := creds.Get("prov"); !has || got != `{"access_token":"fresh"}` {
		t.Fatalf("active key = %q has=%v", got, has)
	}
	if got, has, _ := creds.Get(accountKey("prov", "acc1")); !has || got != `{"access_token":"fresh"}` {
		t.Fatalf("account key = %q has=%v", got, has)
	}
}

func TestPersistCodexTokenSkipsAccountKeyWhenEmpty(t *testing.T) {
	creds := &memCreds{}
	if err := PersistCodexToken(creds, "prov", "", `{"access_token":"fresh"}`); err != nil {
		t.Fatalf("PersistCodexToken: %v", err)
	}
	if ids, _ := creds.ListByPrefix(accountKeyPrefix("prov")); len(ids) != 0 {
		t.Fatalf("unexpected account keys %v", ids)
	}
}

// TestCodexAccountKeyFormat asserts the CredentialStore key layout for
// Codex multi-account storage: "{providerID}:account:" prefix and
// "{providerID}:account:{id}" full key. Moved from domain/codex_keys_test.go
// when the key scheme was relocated to the application layer.
func TestCodexAccountKeyFormat(t *testing.T) {
	cases := []struct {
		providerID string
		accountID  string
		prefix     string
		key        string
	}{
		{"codex", "", "codex:account:", "codex:account:"},
		{"codex", "acc-1", "codex:account:", "codex:account:acc-1"},
		{"openai", "org-123", "openai:account:", "openai:account:org-123"},
	}
	for _, c := range cases {
		if got := accountKeyPrefix(c.providerID); got != c.prefix {
			t.Errorf("accountKeyPrefix(%q) = %q, want %q", c.providerID, got, c.prefix)
		}
		if got := accountKey(c.providerID, c.accountID); got != c.key {
			t.Errorf("accountKey(%q, %q) = %q, want %q", c.providerID, c.accountID, got, c.key)
		}
	}
}

func TestHandleCodexAccountsSwitchUpdatesRouterPreference(t *testing.T) {
	providers := &fakeProviderStore{items: map[string]*domain.Provider{
		"codex": {ID: "codex", Kind: domain.ProviderCodex, Name: "Codex"},
	}}
	credentials := &memCreds{m: map[string]string{
		accountKey("codex", "free"): `{"account_id":"free"}`,
		accountKey("codex", "plus"): `{"account_id":"plus"}`,
	}}
	router := NewCodexAccountRouter()
	router.PickAccount("conv", "codex", []string{"free", "plus"})
	app := &App{Providers: providers, Credentials: credentials, CodexRouter: router, Logs: &fakeLogStore{}, Bus: NewBus()}

	if _, rpcErr := app.handleCodexAccountsSwitch(contracts.CodexAccountsSwitchRequest{ProviderID: "codex", AccountID: "plus"}); rpcErr != nil {
		t.Fatalf("switch account: %v", rpcErr)
	}
	if got := router.PickAccount("conv", "codex", []string{"free", "plus"}); got != "plus" {
		t.Fatalf("conversation account after switch = %q, want plus", got)
	}
}

func TestPrepareCodexTurnAPIKeyHonorsConversationAccountPin(t *testing.T) {
	conversation := &domain.Conversation{ID: "room-plus", ProviderRoute: "plus"}
	router := NewCodexAccountRouter()
	router.PickAccount(conversation.ID, "codex", []string{"free", "plus"})
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{conversation.ID: conversation}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{
			accountKey("codex", "free"): "free-token",
			accountKey("codex", "plus"): "plus-token",
		}},
		CodexRouter: router,
	}
	got, err := app.prepareCodexTurnAPIKey(conversation.ID, &domain.Provider{ID: "codex", Kind: domain.ProviderCodex}, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got != "plus-token" {
		t.Fatalf("token = %q, want selected Plus account", got)
	}
}

func TestHandleProvidersDeleteRemovesAccountCredentials(t *testing.T) {
	provs := &fakeProviderStore{items: map[string]*domain.Provider{
		"prov": {ID: "prov", Kind: domain.ProviderCodex, Name: "Codex"},
	}}
	creds := &memCreds{m: map[string]string{
		"prov":              "active",
		"prov:account:acc1": "tok1",
		"prov:account:acc2": "tok2",
		"other":             "keep",
	}}
	app := &App{Providers: provs, Credentials: creds, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, rpcErr := app.handleProvidersDelete(contracts.ProviderIDRequest{ID: "prov"})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	if m, ok := resp.(map[string]bool); !ok || !m["ok"] {
		t.Fatalf("resp = %#v", resp)
	}
	for _, key := range []string{"prov", "prov:account:acc1", "prov:account:acc2"} {
		if _, has, _ := creds.Get(key); has {
			t.Fatalf("credential %s still present after provider delete", key)
		}
	}
	if _, has, _ := creds.Get("other"); !has {
		t.Fatal("unrelated credential was deleted")
	}
}

func TestApplyUsageToCircuitKeepsOpenWithoutResetAt(t *testing.T) {
	app := &App{CodexRouter: NewCodexAccountRouter(), Logs: &fakeLogStore{}, Bus: NewBus()}
	until := time.Now().Add(time.Hour)
	app.CodexRouter.MarkCircuitOpen("acc1", until)

	app.applyUsageToCircuit("acc1", CodexUsageResult{LimitReached: true})

	got := app.CodexRouter.CircuitOpenUntil("acc1")
	if got.IsZero() {
		t.Fatal("circuit should stay open when LimitReached has no reset timestamp")
	}
}

func TestApplyUsageToCircuitClosesWhenLimitCleared(t *testing.T) {
	app := &App{CodexRouter: NewCodexAccountRouter(), Logs: &fakeLogStore{}, Bus: NewBus()}
	app.CodexRouter.MarkCircuitOpen("acc1", time.Now().Add(time.Hour))

	app.applyUsageToCircuit("acc1", CodexUsageResult{LimitReached: false})

	if !app.CodexRouter.CircuitOpenUntil("acc1").IsZero() {
		t.Fatal("circuit should close when usage is no longer limited")
	}
}
