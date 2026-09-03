package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"nusashell/domain"
)

// mutexSettingsStore is a thread-safe in-memory SettingsStore for tests that
// mutate settings while a wait is in flight (the race detector would flag
// unsynchronized reads/writes otherwise).
type mutexSettingsStore struct {
	mu sync.Mutex
	s  domain.Settings
}

func (m *mutexSettingsStore) Get() domain.Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.s
}

func (m *mutexSettingsStore) Set(s domain.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s = s
	return nil
}

func TestWaitSlowDownDisabledReturnsImmediately(t *testing.T) {
	app := &App{Settings: &mutexSettingsStore{s: domain.DefaultSettings()}} // slow_down = 0
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("wait with slow_down=0 took %v, want immediate return", elapsed)
	}
}

func TestWaitSlowDownAppliesDelay(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.SlowDown = 1
	app := &App{Settings: &mutexSettingsStore{s: settings}}
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed < 850*time.Millisecond {
		t.Fatalf("wait with slow_down=1 took %v, want ~1s", elapsed)
	}
}

// TestWaitSlowDownAbortsOnCancel: user stop / conversation switch / server
// shutdown must cut the wait short instead of blocking the turn.
func TestWaitSlowDownAbortsOnCancel(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.SlowDown = 5
	app := &App{Settings: &mutexSettingsStore{s: settings}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	app.waitSlowDown(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ctx cancel did not cut the wait short: took %v, want < 2s", elapsed)
	}
}

// TestWaitSlowDownClearedMidWait is the live-update contract: saving 0 (or a
// lower value) from the Settings UI while a conversation is mid-delay must
// take effect immediately — no stop, no idle turn, no restart.
func TestWaitSlowDownClearedMidWait(t *testing.T) {
	store := &mutexSettingsStore{s: domain.DefaultSettings()}
	store.s.SlowDown = 5
	app := &App{Settings: store}
	go func() {
		time.Sleep(120 * time.Millisecond)
		s := store.Get()
		s.SlowDown = 0
		_ = store.Set(s)
	}()
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("clearing slow_down mid-wait did not cut it short: took %v, want < 2s", elapsed)
	}
}

// TestWaitSlowDownLoweredMidWait: a reduced value shrinks the remaining wait
// to the newly configured duration (5s → 1s must end after ~1s total, not 5s).
func TestWaitSlowDownLoweredMidWait(t *testing.T) {
	store := &mutexSettingsStore{s: domain.DefaultSettings()}
	store.s.SlowDown = 5
	app := &App{Settings: store}
	go func() {
		time.Sleep(120 * time.Millisecond)
		s := store.Get()
		s.SlowDown = 1
		_ = store.Set(s)
	}()
	start := time.Now()
	app.waitSlowDown(context.Background())
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("lowering slow_down mid-wait did not shrink it: took %v, want < 3s", elapsed)
	}
}

// TestEngineBeforeRoundAppliesSlowDownBetweenRounds: two rounds with
// slow_down=1 must take ~2s wall-clock with both streams still firing — the
// delay slots into the round boundary, never blocks or skips the provider
// call itself.
func TestEngineBeforeRoundAppliesSlowDownBetweenRounds(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.SlowDown = 1
	app := &App{Settings: &mutexSettingsStore{s: settings}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var streams []time.Time
	rules := AgentRules{
		BeforeRound: func(st *RoundState) error {
			app.waitSlowDown(ctx)
			return nil
		},
		Stream: func(ctx context.Context, req ChatRequest) (ChatResponse, error) {
			streams = append(streams, time.Now())
			return ChatResponse{Content: "round"}, nil
		},
		Terminal: func(st *RoundState, resp ChatResponse) bool { return st.Round >= 1 },
		Execute: func(st *RoundState, resp ChatResponse, calls []domain.ToolCall) ([]ToolOutcome, error) {
			return nil, nil
		},
	}
	start := time.Now()
	if _, err := (&AgentEngine{}).Run(ctx, rules, 0); err != nil {
		t.Fatalf("engine run: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2 (delay must not swallow rounds)", len(streams))
	}
	if elapsed := time.Since(start); elapsed < 1850*time.Millisecond {
		t.Fatalf("two rounds with slow_down=1 took %v, want >= ~2s", elapsed)
	}
}
