package application

import (
	"context"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type stubCodexCLIAuth struct {
	tok CodexToken
	err error
}

func (s stubCodexCLIAuth) ImportFromCodexCLI(context.Context) (CodexToken, error) {
	return s.tok, s.err
}

func TestHandleCodexImportMaterializesBuiltinProvider(t *testing.T) {
	provs := &fakeProviderStore{items: map[string]*domain.Provider{}}
	creds := &memCreds{m: map[string]string{}}
	app := &App{
		Providers:    provs,
		Credentials:  creds,
		CodexCLIAuth: stubCodexCLIAuth{tok: CodexToken{AccessToken: "at", RefreshToken: "rt", AccountID: "acc-1", Email: "a@b.c"}},
		Logs:         &fakeLogStore{},
		Bus:          NewBus(),
	}

	res, rpcErr := app.handleCodexImport(contracts.CodexImportRequest{ProviderID: "codex"})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %#v", rpcErr)
	}
	out, ok := res.(contracts.CodexImportResult)
	if !ok || out.AccountID != "acc-1" || out.Skipped {
		t.Fatalf("result = %#v", res)
	}
	p, err := provs.Get("codex")
	if err != nil {
		t.Fatalf("builtin provider not materialized: %v", err)
	}
	if p.Kind != domain.ProviderCodex || p.Driver != domain.ProviderDriverCodex || !p.HasAPIKey {
		t.Fatalf("provider = %#v", p)
	}
	if got, has, _ := creds.Get("codex"); !has || got == "" {
		t.Fatal("active credential missing")
	}
	if _, has, _ := creds.Get(accountKey("codex", "acc-1")); !has {
		t.Fatal("account credential missing")
	}
}

func TestEnsureCodexProviderRejectsUnknownID(t *testing.T) {
	app := &App{Providers: &fakeProviderStore{items: map[string]*domain.Provider{}}, Logs: &fakeLogStore{}, Bus: NewBus()}
	_, rpcErr := app.ensureCodexProvider("missing")
	if rpcErr == nil || rpcErr.Code != contracts.CodeNotFound {
		t.Fatalf("rpcErr = %#v", rpcErr)
	}
}
