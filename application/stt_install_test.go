package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"nusashell/contracts"
)

type fakeSTTInstaller struct {
	mu       sync.Mutex
	status   contracts.STTInstallStatusResult
	started  []string
	failWith error
	block    chan struct{}
}

func (f *fakeSTTInstaller) Status() contracts.STTInstallStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeSTTInstaller) Install(ctx context.Context, modelID string, report func(contracts.STTInstallProgressDTO)) error {
	f.mu.Lock()
	f.started = append(f.started, modelID)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
		}
	}
	report(contracts.STTInstallProgressDTO{Phase: PhaseSTTBinary})
	f.mu.Lock()
	fail := f.failWith
	f.mu.Unlock()
	if fail != nil {
		return fail
	}
	return nil
}

func (f *fakeSTTInstaller) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func sttInstallApp(inst *fakeSTTInstaller) *App {
	app := &App{STTInstaller: inst, Logs: &fakeLogStore{}}
	app.Bus = NewBus()
	return app
}

// STT phase aliases live in the installer package; the application layer
// only sees opaque strings over the wire.
const PhaseSTTBinary = "binary"

func TestSTTInstallStatusReturnsSnapshot(t *testing.T) {
	inst := &fakeSTTInstaller{status: contracts.STTInstallStatusResult{
		Supported:       true,
		EngineInstalled: true,
		Models:          []contracts.STTModelDTO{{ID: "ggml-small", Installed: true, Default: true}},
	}}
	out, rpcErr := sttInstallApp(inst).handleSTTSettingsInstallStatus()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.STTInstallStatusResult)
	if !res.EngineInstalled || !res.Models[0].Installed || res.Running {
		t.Errorf("unexpected status %+v", res)
	}
}

func TestSTTInstallStatusNilInstallerFails(t *testing.T) {
	_, rpcErr := (&App{Logs: &fakeLogStore{}}).handleSTTSettingsInstallStatus()
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "unavailable") {
		t.Fatalf("expected unavailable error, got %v", rpcErr)
	}
}

func TestSTTInstallStartValidatesModel(t *testing.T) {
	inst := &fakeSTTInstaller{}
	app := sttInstallApp(inst)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{}); rpcErr == nil ||
		!strings.Contains(rpcErr.Message, "model_id is required") {
		t.Fatalf("expected empty-model validation, got %v", rpcErr)
	}
	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-nope"}); rpcErr == nil ||
		!strings.Contains(rpcErr.Message, "unknown STT model") {
		t.Fatalf("expected unknown-model validation, got %v", rpcErr)
	}
	if inst.startedCount() != 0 {
		t.Error("installer must not be invoked for invalid requests")
	}
}

func TestSTTInstallDoneEventCarriesModelID(t *testing.T) {
	inst := &fakeSTTInstaller{}
	app := sttInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-small"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	done := waitForEvent(t, events, contracts.EventSTTInstallDone)
	raw, _ := json.Marshal(done.Payload)
	var prog contracts.STTInstallProgressDTO
	if err := json.Unmarshal(raw, &prog); err != nil || prog.ModelID != "ggml-small" {
		t.Fatalf("done payload missing model id: %s (%v)", raw, err)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "install slot must release")
}

func TestSTTInstallErrorEventOnFailure(t *testing.T) {
	inst := &fakeSTTInstaller{failWith: errors.New("disk full")}
	app := sttInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-tiny"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	errEv := waitForEvent(t, events, contracts.EventSTTInstallError)
	rawErr, _ := json.Marshal(errEv.Payload)
	var prog contracts.STTInstallProgressDTO
	if err := json.Unmarshal(rawErr, &prog); err != nil {
		t.Fatalf("error payload decode: %v (%s)", err, rawErr)
	}
	if !strings.Contains(prog.Message, "disk full") {
		t.Errorf("error payload message = %q", prog.Message)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "failed install must release the slot")
}

func TestSTTInstallSingleFlight(t *testing.T) {
	inst := &fakeSTTInstaller{block: make(chan struct{})}
	app := sttInstallApp(inst)

	res, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-small"})
	if rpcErr != nil || !res.(contracts.STTInstallStartResult).Started {
		t.Fatalf("first start: %v %v", res, rpcErr)
	}
	res2, _ := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-tiny"})
	r2 := res2.(contracts.STTInstallStartResult)
	if r2.Started || !r2.Running {
		t.Errorf("second start during flight = %+v, want Started:false Running:true", r2)
	}
	close(inst.block)
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "slot release after unblock")
}

func TestSTTInstallCancelStopsAndReleases(t *testing.T) {
	inst := &fakeSTTInstaller{block: make(chan struct{})}
	app := sttInstallApp(inst)

	if _, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-small"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	out, rpcErr := app.handleSTTSettingsInstallCancel()
	if rpcErr != nil {
		t.Fatalf("cancel: %v", rpcErr)
	}
	if res := out.(contracts.STTInstallStartResult); res.Running {
		t.Errorf("cancel result = %+v, want Running:false", res)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "cancel must release the slot")

	// Slot is free again: a fresh install can start and complete.
	inst.block = nil
	res, rpcErr := app.handleSTTSettingsInstallStart(contracts.STTInstallStartRequest{ModelID: "ggml-base"})
	if rpcErr != nil || !res.(contracts.STTInstallStartResult).Started {
		t.Fatalf("restart after cancel: %v %v", res, rpcErr)
	}
	waitForCondition(t, func() bool { return !app.sttInstallRunning() }, "second install must finish")
}

func TestSTTInstallCancelWithoutRunIsNoop(t *testing.T) {
	app := sttInstallApp(&fakeSTTInstaller{})
	out, rpcErr := app.handleSTTSettingsInstallCancel()
	if rpcErr != nil {
		t.Fatalf("cancel noop: %v", rpcErr)
	}
	if out.(contracts.STTInstallStartResult).Running {
		t.Error("noop cancel must not report running")
	}
}

// compile-time interface check mirrors the TTS port contract.
var _ STTInstaller = (*fakeSTTInstaller)(nil)
