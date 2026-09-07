package application

import (
	"errors"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func codexImageFailoverApp(t *testing.T, accounts map[string]string) *App {
	t.Helper()
	m := map[string]string{"prov": "tok-active"}
	for id, tok := range accounts {
		m[accountKey("prov", id)] = tok
	}
	return &App{
		CodexRouter: NewCodexAccountRouter(),
		Credentials: &memCreds{m: m},
		Logs:        &fakeLogStore{},
		Bus:         NewBus(),
	}
}

func setCodexStickyAccount(t *testing.T, app *App, conversationID string) string {
	t.Helper()
	accounts := app.listCodexAccountIDs("prov")
	if len(accounts) == 0 {
		t.Fatal("no codex accounts stored")
	}
	pick := app.CodexRouter.PickAccountDetailed(conversationID, "prov", accounts)
	return pick.AccountID
}

func TestFailoverCodexOnImageErrorUsageLimitMovesAccount(t *testing.T) {
	app := codexImageFailoverApp(t, map[string]string{"a1": "tok-a1", "a2": "tok-a2"})
	current := setCodexStickyAccount(t, app, "conv1")
	other := "a2"
	if current == "a2" {
		other = "a1"
	}
	resetAt := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	newKey, retry, replaced := app.failoverCodexOnImageError(nil, "conv1",
		&domain.Provider{ID: "prov", Kind: domain.ProviderCodex}, "tok-active",
		&domain.ProviderError{StatusCode: 429, UsageLimitResetAt: resetAt, Err: errors.New("image generation usage limit reached")})
	if !retry || replaced != nil {
		t.Fatalf("retry = %v replaced = %v", retry, replaced)
	}
	if newKey != "tok-"+other {
		t.Fatalf("newKey = %q, want tok-%s", newKey, other)
	}
	if until := app.CodexRouter.CircuitOpenUntil(current); !until.Equal(resetAt) {
		t.Fatalf("circuit %s until = %s, want %s", current, until, resetAt)
	}
	if sticky := app.CodexRouter.StickyAccount("conv1"); sticky != other {
		t.Fatalf("sticky = %q, want %s after failover", sticky, other)
	}
}

func TestFailoverCodexOnImageErrorPlain429MovesAccount(t *testing.T) {
	app := codexImageFailoverApp(t, map[string]string{"a1": "tok-a1", "a2": "tok-a2"})
	current := setCodexStickyAccount(t, app, "conv1")
	newKey, retry, replaced := app.failoverCodexOnImageError(nil, "conv1",
		&domain.Provider{ID: "prov", Kind: domain.ProviderCodex}, "tok-active",
		&domain.ProviderError{StatusCode: 429, Err: errors.New("image generation rate limited")})
	if !retry || replaced != nil || newKey == "tok-"+current {
		t.Fatalf("retry = %v replaced = %v newKey = %q current = %s", retry, replaced, newKey, current)
	}
	// The first account is now blocked; a new conversation pick skips it.
	if got := setCodexStickyAccount(t, app, "conv2"); got == current {
		t.Fatalf("pick after 429 = %q, want a different account", got)
	}
}

func TestFailoverCodexOnImageErrorRespectsConversationAccountPin(t *testing.T) {
	app := codexImageFailoverApp(t, map[string]string{"a1": "tok-a1", "a2": "tok-a2"})
	current := setCodexStickyAccount(t, app, "conv-pinned")
	app.Conversations = &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv-pinned": {ID: "conv-pinned", ProviderRoute: current},
	}}

	for _, genErr := range []error{
		&domain.ProviderError{StatusCode: 429, Err: errors.New("image generation rate limited")},
		&domain.ProviderError{StatusCode: 403, Err: errors.New("image generation forbidden")},
	} {
		newKey, retry, replaced := app.failoverCodexOnImageError(nil, "conv-pinned",
			&domain.Provider{ID: "prov", Kind: domain.ProviderCodex}, "tok-active", genErr)
		if retry || newKey != "" || replaced != nil {
			t.Fatalf("pinned room error %v: retry=%v newKey=%q replaced=%v", genErr, retry, newKey, replaced)
		}
		if sticky := app.CodexRouter.StickyAccount("conv-pinned"); sticky != current {
			t.Fatalf("pinned room sticky = %q, want %q", sticky, current)
		}
		if until := app.CodexRouter.CircuitOpenUntil(current); !until.IsZero() {
			t.Fatalf("pinned room circuit = %s, want unchanged", until)
		}
	}
}

func TestFailoverCodexOnImageError403MovesAccount(t *testing.T) {
	app := codexImageFailoverApp(t, map[string]string{"a1": "tok-a1", "a2": "tok-a2"})
	current := setCodexStickyAccount(t, app, "conv1")
	newKey, retry, replaced := app.failoverCodexOnImageError(nil, "conv1",
		&domain.Provider{ID: "prov", Kind: domain.ProviderCodex}, "tok-active",
		&domain.ProviderError{StatusCode: 403, Err: errors.New(`image generation failed (HTTP 403): {"detail":"Forbidden"}`)})
	if !retry || replaced != nil || newKey == "tok-"+current {
		t.Fatalf("retry = %v replaced = %v newKey = %q current = %s", retry, replaced, newKey, current)
	}
	until := app.CodexRouter.CircuitOpenUntil(current)
	if until.IsZero() || until.Before(time.Now().Add(23*time.Hour)) {
		t.Fatalf("circuit %s until = %s, want ~24h block", current, until)
	}
}

func TestFailoverCodexOnImageErrorAllLimited(t *testing.T) {
	app := codexImageFailoverApp(t, map[string]string{"a1": "tok-a1"})
	if got := setCodexStickyAccount(t, app, "conv1"); got != "a1" {
		t.Fatalf("sticky = %q, want a1", got)
	}
	newKey, retry, replaced := app.failoverCodexOnImageError(nil, "conv1",
		&domain.Provider{ID: "prov", Kind: domain.ProviderCodex}, "tok-active",
		&domain.ProviderError{StatusCode: 429, UsageLimitResetAt: time.Now().Add(time.Hour), Err: errors.New("image generation usage limit reached")})
	if retry || newKey != "" || replaced == nil {
		t.Fatalf("retry = %v newKey = %q replaced = %v", retry, newKey, replaced)
	}
	if !strings.Contains(replaced.Error(), "all Codex accounts are rate-limited") {
		t.Fatalf("replaced = %v", replaced)
	}
}

func TestFailoverCodexOnImageErrorIgnoresOtherErrors(t *testing.T) {
	app := codexImageFailoverApp(t, map[string]string{"a1": "tok-a1", "a2": "tok-a2"})
	setCodexStickyAccount(t, app, "conv1")
	for _, err := range []error{
		&domain.ProviderError{StatusCode: 500, Temporary: true, Err: errors.New("overloaded")},
		errors.New("transport: dial tcp: connection refused"),
	} {
		newKey, retry, replaced := app.failoverCodexOnImageError(nil, "conv1",
			&domain.Provider{ID: "prov", Kind: domain.ProviderCodex}, "tok-active", err)
		if retry || newKey != "" || replaced != nil {
			t.Fatalf("err=%v → retry=%v newKey=%q replaced=%v", err, retry, newKey, replaced)
		}
	}
}

func TestFailoverCodexOnImageErrorSkipsNonCodex(t *testing.T) {
	app := codexImageFailoverApp(t, map[string]string{"a1": "tok-a1"})
	newKey, retry, replaced := app.failoverCodexOnImageError(nil, "conv1",
		&domain.Provider{ID: "prov", Kind: domain.ProviderChat}, "tok",
		&domain.ProviderError{StatusCode: 429, Err: errors.New("rate limited")})
	if retry || newKey != "" || replaced != nil {
		t.Fatalf("retry = %v newKey = %q replaced = %v", retry, newKey, replaced)
	}
}
