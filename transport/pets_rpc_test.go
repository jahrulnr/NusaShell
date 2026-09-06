package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
)

// fakePetsInstaller is the transport-layer double for application.PetsInstaller.
// The same shape as the application test double so RPCs round-trip the wire
// shape (PetsStatusResult, PetsInstallStartResult, PetsLaunchResult) without
// touching the real installer.
type fakePetsInstaller struct {
	mu         sync.Mutex
	status     contracts.PetsStatusResult
	installs   []string
	launches   int
	launchPath string
	launchErr  error
	failWith   error
	block      chan struct{}
}

func (f *fakePetsInstaller) Status() contracts.PetsStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakePetsInstaller) Install(ctx context.Context, version string, report func(contracts.PetsInstallProgressDTO)) error {
	f.mu.Lock()
	f.installs = append(f.installs, version)
	block := f.block
	fail := f.failWith
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
		}
	}
	if report != nil {
		report(contracts.PetsInstallProgressDTO{Phase: "verify"})
	}
	if fail != nil {
		return fail
	}
	return nil
}

func (f *fakePetsInstaller) Launch() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches++
	if f.launchErr != nil {
		return "", f.launchErr
	}
	return f.launchPath, nil
}

// rpcHelper wraps the real transport.Server so this test can drive /rpc
// without rebuilding the full agent harness.
type rpcHelper struct {
	server *httptest.Server
	app    *application.App
}

func newRPCHelper(t *testing.T, app *application.App) *rpcHelper {
	t.Helper()
	logger := newDiscardLogger()
	srv := newRPCHelperServer(t, app, logger)
	t.Cleanup(srv.Close)
	return &rpcHelper{server: srv, app: app}
}

func newRPCHelperServer(t *testing.T, app *application.App, logger *slog.Logger) *httptest.Server {
	t.Helper()
	srv := New(app, logger, StaticHandler(nil, true), true)
	return httptest.NewServer(srv.Routes())
}

func (h *rpcHelper) rpc(method string, payload any) (map[string]any, *contracts.RPCError) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/rpc/"+method, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	var out struct {
		Result any                 `json:"result,omitempty"`
		Error  *contracts.RPCError `json:"error,omitempty"`
	}
	if err := dec.Decode(&out); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if out.Error != nil {
		return nil, out.Error
	}
	if out.Result == nil {
		return map[string]any{}, nil
	}
	switch v := out.Result.(type) {
	case map[string]any:
		return v, nil
	default:
		data, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		return m, nil
	}
}

func (h *rpcHelper) waitInstallDone(t *testing.T, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if !h.app.PetsInstallRunning() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("install never finished")
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

type discardLogStore struct{}

func newDiscardLogStore() *discardLogStore { return &discardLogStore{} }

func (*discardLogStore) Append(*domain.LogEntry)             {}
func (*discardLogStore) List(string, int) []*domain.LogEntry { return nil }
func (*discardLogStore) Clear()                              {}

type memSettingsStore struct{ v domain.Settings }

func newMemSettingsStore() *memSettingsStore { return &memSettingsStore{v: domain.DefaultSettings()} }

func (m *memSettingsStore) Get() domain.Settings        { return m.v }
func (m *memSettingsStore) Set(s domain.Settings) error { m.v = s; return nil }

func petsApp(inst *fakePetsInstaller) *application.App {
	return &application.App{
		PetsInstaller: inst,
		Logs:          newDiscardLogStore(),
		Settings:      newMemSettingsStore(),
		Bus:           application.NewBus(),
	}
}

func TestRPCPetsStatusReturnsSnapshot(t *testing.T) {
	inst := &fakePetsInstaller{status: contracts.PetsStatusResult{
		Supported: runtime.GOOS == "linux",
		Installed: true,
		Path:      "/opt/pets/current/nusashell-pets",
		Version:   "0.2.0",
	}}
	helper := newRPCHelper(t, petsApp(inst))
	res, rpcErr := helper.rpc(contracts.MethodPetsStatus, map[string]any{})
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	if res["installed"] != true {
		t.Errorf("installed must mirror installer snapshot, got %v", res["installed"])
	}
	if res["path"] != "/opt/pets/current/nusashell-pets" {
		t.Errorf("path mismatch: %v", res["path"])
	}
	if res["version"] != "0.2.0" {
		t.Errorf("version mismatch: %v", res["version"])
	}
}

func TestRPCPetsInstallSingleFlight(t *testing.T) {
	inst := &fakePetsInstaller{
		status: contracts.PetsStatusResult{Supported: true},
		block:  make(chan struct{}),
	}
	app := petsApp(inst)
	helper := newRPCHelper(t, app)

	first, err := helper.rpc(contracts.MethodPetsInstallStart, map[string]any{})
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if first["started"] != true {
		t.Fatalf("first start must begin: %v", first)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if app.PetsInstallRunning() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !app.PetsInstallRunning() {
		close(inst.block)
		t.Fatal("first install never reached active state")
	}

	second, err := helper.rpc(contracts.MethodPetsInstallStart, map[string]any{})
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if second["started"] != false || second["running"] != true {
		t.Errorf("second concurrent start must report running=true started=false, got %v", second)
	}
	close(inst.block)
	helper.waitInstallDone(t, 2*time.Second)
}

func TestRPCPetsInstallErrorSurfacesOnBus(t *testing.T) {
	inst := &fakePetsInstaller{
		status:   contracts.PetsStatusResult{Supported: true},
		failWith: errors.New("network down"),
	}
	app := petsApp(inst)
	helper := newRPCHelper(t, app)

	events := make(chan contracts.Event, 4)
	_, sub, cancel := app.Bus.Subscribe()
	defer cancel()
	go func() {
		for ev := range sub {
			events <- ev
		}
	}()

	if _, err := helper.rpc(contracts.MethodPetsInstallStart, map[string]any{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case ev := <-events:
		if ev.Type != contracts.EventPetsInstallError {
			// Skip interleaved progress events from the same goroutine.
			for ev.Type != contracts.EventPetsInstallError {
				select {
				case ev = <-events:
				case <-time.After(2 * time.Second):
					t.Fatal("error event not emitted")
				}
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error event not emitted")
	}
	helper.waitInstallDone(t, 2*time.Second)
}

func TestRPCPetsLaunchSurfacesSpawnFailure(t *testing.T) {
	inst := &fakePetsInstaller{
		status: contracts.PetsStatusResult{
			Supported: true,
			Installed: true,
			Path:      "/opt/pets/nusashell-pets",
		},
		launchErr: errors.New("spawn failed"),
	}
	helper := newRPCHelper(t, petsApp(inst))
	res, err := helper.rpc(contracts.MethodPetsLaunch, map[string]any{})
	if err != nil {
		t.Fatalf("rpc: %v", err)
	}
	if res["launched"] != false {
		t.Errorf("launched must be false, got %v", res["launched"])
	}
	if msg, _ := res["message"].(string); !strings.Contains(msg, "spawn failed") {
		t.Errorf("expected error message, got %q", msg)
	}
}

func TestRPCPetsLaunchSuccess(t *testing.T) {
	inst := &fakePetsInstaller{
		status: contracts.PetsStatusResult{
			Supported: true,
			Installed: true,
			Path:      "/opt/pets/nusashell-pets",
		},
		launchPath: "/opt/pets/nusashell-pets",
	}
	helper := newRPCHelper(t, petsApp(inst))
	res, err := helper.rpc(contracts.MethodPetsLaunch, map[string]any{})
	if err != nil {
		t.Fatalf("rpc: %v", err)
	}
	if res["launched"] != true {
		t.Errorf("launched must be true, got %v", res)
	}
	if res["path"] != "/opt/pets/nusashell-pets" {
		t.Errorf("path mismatch: %v", res)
	}
	if inst.launches != 1 {
		t.Errorf("installer.Launch must be called once, got %d", inst.launches)
	}
}
