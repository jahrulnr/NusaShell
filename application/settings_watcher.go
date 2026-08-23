package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// settingsWatchInterval is the poll cadence for config/settings.json. Two
// seconds is instant enough for interactive hand edits and negligible for an
// idle personal shell (one stat per tick, no reads unless the file changed).
const settingsWatchInterval = 2 * time.Second

// settingsApplyStore is the optional capability behind the Settings port:
// hot-swap in-memory settings without a disk write-back. The jsonstore
// adapter implements it; test fakes that only implement Get/Set keep working
// and the watcher degrades to Set (which rewrites the file with identical
// content — harmless, just noisier mtimes).
type settingsApplyStore interface {
	ApplySettings(s domain.Settings)
}

// SettingsWatcher implements Laravel-style config watching for
// config/settings.json: poll modified-vs-last-modified, on change validate,
// valid reloads in-memory (disk stays source of truth), invalid keeps the
// previously active values and logs why. A broken hand edit never crashes or
// poisons the running shell.
//
// Self-writes are silent by construction: settings.set writes exactly what is
// already in memory, so parse+normalize yields a value equal to the current
// one and nothing is emitted.
type SettingsWatcher struct {
	app      *App
	interval time.Duration

	mu    sync.Mutex
	state settingsFileState // last seen file fingerprint
}

// settingsFileState fingerprints the watched file between ticks. size guards
// same-mtime rewrites; valid=false until first successful read.
type settingsFileState struct {
	modNano int64
	size    int64
	valid   bool
}

// NewSettingsWatcher builds a watcher over app.DataDir/config/settings.json.
func NewSettingsWatcher(app *App) *SettingsWatcher {
	return &SettingsWatcher{app: app, interval: settingsWatchInterval}
}

func (w *SettingsWatcher) path() string {
	return filepath.Join(w.app.DataDir, "config", "settings.json")
}

func (w *SettingsWatcher) fingerprint() (settingsFileState, bool) {
	fi, err := os.Stat(w.path())
	if err != nil {
		return settingsFileState{}, false
	}
	return settingsFileState{modNano: fi.ModTime().UnixNano(), size: fi.Size(), valid: true}, true
}

// Run polls until ctx is cancelled. The first tick records the baseline
// without emitting: startup already loaded this exact file into memory.
func (w *SettingsWatcher) Run(ctx context.Context) {
	if w.app.DataDir == "" || w.app.Settings == nil {
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check()
		}
	}
}

// check compares the current fingerprint against the last handled one and
// processes the file when it differs. One attempt per change: state advances
// whether the content validates or not, so a permanently broken edit logs
// once instead of every tick; fixing the file produces a new change.
func (w *SettingsWatcher) check() {
	w.mu.Lock()
	defer w.mu.Unlock()

	cur, ok := w.fingerprint()
	if !ok {
		// File removed mid-run (editor replace window): keep runtime values,
		// reset so reappearance is treated as a fresh change.
		w.state = settingsFileState{}
		return
	}
	if cur == w.state {
		return
	}
	w.state = cur

	b, err := os.ReadFile(w.path())
	if err != nil {
		w.reject("read failed: " + err.Error())
		return
	}
	var parsed domain.Settings
	if err := json.Unmarshal(b, &parsed); err != nil {
		w.reject("invalid JSON: " + err.Error())
		return
	}
	next := domain.NormalizeSettings(parsed)
	current := w.app.Settings.Get()
	if reflect.DeepEqual(domain.NormalizeSettings(current), next) {
		return // self-write or cosmetic rewrite: already live, stay silent
	}
	w.apply(next)
}

func (w *SettingsWatcher) apply(s domain.Settings) {
	if as, ok := w.app.Settings.(settingsApplyStore); ok {
		as.ApplySettings(s)
	} else if err := w.app.Settings.Set(s); err != nil {
		w.reject("apply failed: " + err.Error())
		return
	}
	w.app.log("info", "settings-watch", "settings.json changed on disk — reloaded into runtime")
	if w.app.Bus != nil {
		w.app.Bus.Emit(contracts.EventSettingsApplied, contracts.SettingsAppliedEvent{})
	}
}

func (w *SettingsWatcher) reject(reason string) {
	w.app.log("warn", "settings-watch", "external settings change skipped (%s) — previous values stay active", reason)
	if w.app.Bus != nil {
		w.app.Bus.Emit(contracts.EventSettingsRejected, contracts.SettingsRejectedEvent{Reason: reason})
	}
}

// StartSettingsWatcher arms the polling loop on the given lifecycle context;
// cancellation stops it. No-op without a data dir or settings store.
func (a *App) StartSettingsWatcher(ctx context.Context) {
	if a.Settings == nil || a.DataDir == "" {
		return
	}
	a.goSafe("settings-watch", func() { NewSettingsWatcher(a).Run(ctx) })
	a.log("info", "settings-watch", "watching %s for external edits (poll %s)", filepath.Join(a.DataDir, "config", "settings.json"), settingsWatchInterval)
}
