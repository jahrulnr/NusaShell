package application

import (
	"context"
	"encoding/json"
	"errors"
	"nusashell/contracts"
	"nusashell/domain"
	"testing"
	"time"
)

type deviceOAuthFake struct {
	token *CodexToken
	err   error
	polls int
}

func (f *deviceOAuthFake) Login(context.Context) (CodexToken, error) {
	return CodexToken{}, errors.New("browser login must not run")
}
func (f *deviceOAuthFake) ExtractProfile(string) (string, string) { return "", "" }
func (f *deviceOAuthFake) StartDeviceLogin(ctx context.Context) (CodexDeviceCode, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("missing timeout")
	}
	return CodexDeviceCode{DeviceAuthID: "device", UserCode: "CODE", VerificationURL: "https://auth.openai.com/codex/device", IntervalSeconds: 5, ExpiresAt: time.Now().Add(time.Minute).Unix()}, f.err
}
func (f *deviceOAuthFake) PollDeviceLogin(ctx context.Context, id, code string) (*CodexToken, error) {
	f.polls++
	return f.token, f.err
}
func TestCodexDeviceDispatchPersistsOnlyAfterCompletion(t *testing.T) {
	oauth := &deviceOAuthFake{}
	creds := &memCreds{m: map[string]string{}}
	app := &App{Providers: &fakeProviderStore{items: map[string]*domain.Provider{}}, Credentials: creds, CodexOAuth: oauth, Logs: &fakeLogStore{}, Bus: NewBus()}
	raw := json.RawMessage(`{"provider_id":"codex"}`)
	started, rpcErr := app.dispatchAI(contracts.MethodCodexDeviceStart, raw)
	if rpcErr != nil || started.(contracts.CodexDeviceStartResult).UserCode != "CODE" {
		t.Fatalf("start: %+v %+v", started, rpcErr)
	}
	raw = json.RawMessage(`{"provider_id":"codex","device_auth_id":"device","user_code":"CODE"}`)
	pending, rpcErr := app.dispatchAI(contracts.MethodCodexDevicePoll, raw)
	if rpcErr != nil || pending.(contracts.CodexDevicePollResult).Status != "pending" {
		t.Fatalf("pending: %+v %+v", pending, rpcErr)
	}
	if _, has, _ := creds.Get("codex"); has {
		t.Fatal("pending login stored credentials")
	}
	oauth.token = &CodexToken{AccessToken: "access", RefreshToken: "refresh", AccountID: "account"}
	complete, rpcErr := app.dispatchAI(contracts.MethodCodexDevicePoll, raw)
	if rpcErr != nil || complete.(contracts.CodexDevicePollResult).AccountID != "account" {
		t.Fatalf("complete: %+v %+v", complete, rpcErr)
	}
	active, has, _ := creds.Get("codex")
	scoped, _, _ := creds.Get(accountKey("codex", "account"))
	if !has || active != scoped {
		t.Fatal("completed token not persisted under both keys")
	}
	oauth.err = errors.New("device rejected")
	if _, err := app.dispatchAI(contracts.MethodCodexDevicePoll, raw); err == nil || err.Code != contracts.CodeProvider {
		t.Fatalf("provider error = %+v", err)
	}
	calls := oauth.polls
	if _, err := app.dispatchAI(contracts.MethodCodexDevicePoll, json.RawMessage(`{"provider_id":"codex"}`)); err == nil || err.Code != contracts.CodeValidation {
		t.Fatalf("invalid request = %+v", err)
	}
	if oauth.polls != calls {
		t.Fatal("invalid request reached adapter")
	}
}
