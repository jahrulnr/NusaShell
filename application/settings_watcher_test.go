package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// watchSettingsStore is a full SettingsStore + hot-swap fake for watcher
// tests. Distinct from the smaller fakes elsewhere so adding methods here
// never collides.
type watchSettingsStore struct {
	mu      sync.Mutex
	cur     domain.Settings
	applies int
	sets    int
}

func (f *watchSettingsStore) Get() domain.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

func (f *watchSettingsStore) Set(s domain.Settings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cur = s
	f.sets++
	return nil
}

func (f *watchSettingsStore) ApplySettings(s domain.Settings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cur = s
	f.applies++
}

func (f *watchSettingsStore) counts() (applies, sets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applies, f.sets
}

type watchFixture struct {
	app    *App
	store  *watchSettingsStore
	events <-chan contracts.Event
	cancel context.CancelFunc
}

func startWatchFixture(t *testing.T) *watchFixture {
	t.Helper()
	bus := NewBus()
	store := &watchSettingsStore{cur: domain.DefaultSettings()}
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := &App{DataDir: dataDir, Bus: bus, Settings: store}
	_, events, unsub := bus.Subscribe()
	t.Cleanup(unsub)
	ctx, cancel := context.WithCancel(context.Background())
	w := NewSettingsWatcher(app)
	go func() { _ = ctx.Err(); w.Run(ctx) }() //nolint:govet // ctx kept alive via cancel fixture
	t.Cleanup(cancel)
	return &watchFixture{app: app, store: store, events: events, cancel: cancel}
}

func waitForEventType(t *testing.T, fx *watchFixture, typ string, timeout time.Duration) contracts.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-fx.events:
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s event", typ)
		}
	}
}

func assertNoEvents(t *testing.T, fx *watchFixture, quiet time.Duration) {
	t.Helper()
	select {
	case ev := <-fx.events:
		t.Fatalf("unexpected event %q", ev.Type)
	case <-time.After(quiet):
	}
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// External valid change: applied in-memory via hot-swap (not Set), event
// emitted, log written.
func TestSettingsWatcherAppliesValidChange(t *testing.T) {
	fx := startWatchFixture(t)
	path := filepath.Join(fx.app.DataDir, "config", "settings.json")
	// Disk keys are the struct's marshal names: legacy fields keep Go-style
	// names (no json tag), newer fields use their snake_case tags.
	writeFileAt(t, path, `{"MaxToolRounds":42}`)

	ev := waitForEventType(t, fx, contracts.EventSettingsApplied, 3*time.Second)
	var payload contracts.SettingsAppliedEvent
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got := fx.store.Get().MaxToolRounds; got != 42 {
		t.Fatalf("MaxToolRounds = %d, want 42", got)
	}
	if applies, sets := fx.store.counts(); applies != 1 || sets != 0 {
		t.Fatalf("applies=%d sets=%d, want 1/0 (hot swap, no write-back)", applies, sets)
	}
}

// Broken JSON: rejected event, previous values stay active.
func TestSettingsWatcherRejectsInvalidJSON(t *testing.T) {
	fx := startWatchFixture(t)
	path := filepath.Join(fx.app.DataDir, "config", "settings.json")
	writeFileAt(t, path, `{"MaxToolRounds":`)

	ev := waitForEventType(t, fx, contracts.EventSettingsRejected, 3*time.Second)
	var payload contracts.SettingsRejectedEvent
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.Reason == "" {
		t.Fatal("expected non-empty rejection reason")
	}
	if got := fx.store.Get().MaxToolRounds; got != domain.DefaultSettings().MaxToolRounds {
		t.Fatalf("store mutated on reject: MaxToolRounds=%d", got)
	}
}

// A rewrite whose parsed+normalized value equals the live settings is a
// no-op: this is what our own settings.set writes look like to the watcher.
func TestSettingsWatcherStaysSilentOnIdenticalRewrite(t *testing.T) {
	fx := startWatchFixture(t)
	b, err := json.Marshal(domain.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fx.app.DataDir, "config", "settings.json")
	writeFileAt(t, path, string(b))

	time.Sleep(150 * time.Millisecond) // a few ticks
	assertNoEvents(t, fx, 300*time.Millisecond)
}

// Missing file mid-run keeps runtime values and emits nothing.
func TestSettingsWatcherToleratesMissingFile(t *testing.T) {
	fx := startWatchFixture(t)
	path := filepath.Join(fx.app.DataDir, "config", "settings.json")
	writeFileAt(t, path, `{"MaxToolRounds":7}`)
	waitForEventType(t, fx, contracts.EventSettingsApplied, 3*time.Second)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	assertNoEvents(t, fx, 300*time.Millisecond)
	if got := fx.store.Get().MaxToolRounds; got != 7 {
		t.Fatalf("values lost after file removal: %d", got)
	}

	// Reappearance is a fresh change again.
	writeFileAt(t, path, `{"MaxToolRounds":9}`)
	waitForEventType(t, fx, contracts.EventSettingsApplied, 3*time.Second)
	if got := fx.store.Get().MaxToolRounds; got != 9 {
		t.Fatalf("reappeared change not applied: %d", got)
	}
}
