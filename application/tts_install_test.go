package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
)

type fakeTTSInstaller struct {
	mu       sync.Mutex
	status   contracts.TTSInstallStatusResult
	started  []string
	failWith error
	block    chan struct{}
}

// startedCount reads the started slice under the mutex (race-safe).
func (f *fakeTTSInstaller) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeTTSInstaller) Status() contracts.TTSInstallStatusResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeTTSInstaller) Install(ctx context.Context, voiceID string, report func(contracts.TTSInstallProgressDTO)) error {
	f.mu.Lock()
	f.started = append(f.started, voiceID)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
		}
	}
	report(contracts.TTSInstallProgressDTO{VoiceID: voiceID, Phase: "binary"})
	f.mu.Lock()
	fail := f.failWith
	f.mu.Unlock()
	if fail != nil {
		return fail
	}
	return nil
}

func ttsInstallApp(inst *fakeTTSInstaller) *App {
	app := &App{TTSInstaller: inst, Logs: &fakeLogStore{}}
	app.Bus = NewBus()
	return app
}

func waitForCondition(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestTTSInstallStatusReturnsSnapshot(t *testing.T) {
	inst := &fakeTTSInstaller{status: contracts.TTSInstallStatusResult{
		BinaryInstalled: true,
		Voices:          []contracts.TTSVoiceDTO{{ID: "id_ID-news_tts-medium", Installed: true}},
	}}
	out, rpcErr := ttsInstallApp(inst).handleTTSSettingsInstallStatus()
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	res := out.(contracts.TTSInstallStatusResult)
	if !res.BinaryInstalled || !res.Voices[0].Installed || res.Running {
		t.Errorf("unexpected status %+v", res)
	}
}

func TestTTSInstallStartRejectsUnknownVoiceImmediately(t *testing.T) {
	inst := &fakeTTSInstaller{}
	app := ttsInstallApp(inst)
	_, rpcErr := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "nope"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "unknown voice") {
		t.Fatalf("expected unknown-voice validation error, got %v", rpcErr)
	}
	if len(inst.started) != 0 {
		t.Error("installer must not be invoked for unknown voice")
	}
}

func collectEvents(t *testing.T, app *App) chan contracts.Event {
	t.Helper()
	ch := make(chan contracts.Event, 16)
	_, sub, stop := app.Bus.Subscribe()
	go func() {
		defer close(ch)
		for ev := range sub {
			ch <- ev
		}
	}()
	t.Cleanup(stop)
	return ch
}

func waitForEvent(t *testing.T, events chan contracts.Event, typ string) contracts.Event {
	t.Helper()
	for {
		select {
		case ev := <-events:
			if ev.Type == typ {
				return ev
			} // skip interleaved progress events
		case <-time.After(2 * time.Second):
			t.Fatalf("%s event not emitted", typ)
		}
	}
}

func TestTTSInstallDoneEventCarriesVoiceID(t *testing.T) {
	inst := &fakeTTSInstaller{}
	app := ttsInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "id_ID-news_tts-medium"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	var done contracts.Event
	done = waitForEvent(t, events, contracts.EventTTSInstallDone)
	if done.Type != contracts.EventTTSInstallDone {
		t.Fatalf("unexpected event %q", done.Type)
	}
	payload, _ := json.Marshal(done.Payload)
	var prog contracts.TTSInstallProgressDTO
	if err := json.Unmarshal(payload, &prog); err != nil || prog.VoiceID != "id_ID-news_tts-medium" {
		t.Fatalf("done payload missing voice id: %s (%v)", payload, err)
	}
	waitForCondition(t, func() bool { return !app.ttsInstallRunning() }, "run flag never cleared")
}

func TestTTSInstallErrorSurfacesOnBus(t *testing.T) {
	inst := &fakeTTSInstaller{failWith: errors.New("disk full")}
	app := ttsInstallApp(inst)
	events := collectEvents(t, app)

	if _, rpcErr := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "id_ID-news_tts-medium"}); rpcErr != nil {
		t.Fatalf("start: %v", rpcErr)
	}
	ev := waitForEvent(t, events, contracts.EventTTSInstallError)
	if ev.Type != contracts.EventTTSInstallError {
		t.Fatalf("unexpected event %q", ev.Type)
	}
}

func TestTTSInstallSingleFlight(t *testing.T) {
	inst := &fakeTTSInstaller{block: make(chan struct{})}
	app := ttsInstallApp(inst)

	first, _ := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "en_US-lessac-high"})
	if !(first.(contracts.TTSInstallStartResult)).Started {
		t.Fatal("first start must begin the install")
	}
	waitForCondition(t, func() bool { return inst.startedCount() == 1 }, "first install never started")

	second, _ := app.handleTTSSettingsInstallStart(contracts.TTSInstallStartRequest{VoiceID: "en_US-lessac-high"})
	res := second.(contracts.TTSInstallStartResult)
	if res.Started || !res.Running {
		t.Errorf("second concurrent start must report running=true started=false, got %+v", res)
	}
	close(inst.block)
	waitForCondition(t, func() bool { return !app.ttsInstallRunning() }, "install never finished")
}
