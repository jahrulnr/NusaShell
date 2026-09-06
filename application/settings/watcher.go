package settings

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
	clock "nusashell/pkg/time"
)

// WatchInterval is the poll cadence for config/settings.json.
const WatchInterval = 2 * time.Second

type fileState struct {
	modNano int64
	size    int64
	valid   bool
}

// Watcher implements Laravel-style config watching for
// config/settings.json: poll modified-vs-last-modified, on change validate,
// valid reloads in-memory (disk stays source of truth), invalid keeps the
// previously active values and logs why.
type Watcher struct {
	dataDir  string
	store    Store
	bus      Emitter
	log      Logger
	interval time.Duration

	mu    sync.Mutex
	state fileState
}

// WatcherDeps is the narrow wiring for NewWatcher.
type WatcherDeps struct {
	DataDir  string
	Store    Store
	Bus      Emitter
	Log      Logger
	Interval time.Duration
}

// NewWatcher builds a watcher over dataDir/config/settings.json.
func NewWatcher(d WatcherDeps) *Watcher {
	interval := d.Interval
	if interval <= 0 {
		interval = WatchInterval
	}
	return &Watcher{
		dataDir:  d.DataDir,
		store:    d.Store,
		bus:      d.Bus,
		log:      d.Log,
		interval: interval,
	}
}

func (w *Watcher) path() string {
	return filepath.Join(w.dataDir, "config", "settings.json")
}

func (w *Watcher) fingerprint() (fileState, bool) {
	fi, err := os.Stat(w.path())
	if err != nil {
		return fileState{}, false
	}
	return fileState{modNano: clock.NewTime(fi.ModTime()).EpochNano(), size: fi.Size(), valid: true}, true
}

func (w *Watcher) write(level, format string, args ...any) {
	if w.log != nil {
		w.log(level, "settings-watch", format, args...)
	}
}

// Run polls until ctx is cancelled. The first tick records the baseline
// without emitting: startup already loaded this exact file into memory.
func (w *Watcher) Run(ctx context.Context) {
	if w.dataDir == "" || w.store == nil {
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

func (w *Watcher) check() {
	w.mu.Lock()
	defer w.mu.Unlock()

	cur, ok := w.fingerprint()
	if !ok {
		w.state = fileState{}
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
	current := w.store.Get()
	if reflect.DeepEqual(domain.NormalizeSettings(current), next) {
		return
	}
	w.apply(next)
}

func (w *Watcher) apply(s domain.Settings) {
	if as, ok := w.store.(ApplyStore); ok {
		as.ApplySettings(s)
	} else if err := w.store.Set(s); err != nil {
		w.reject("apply failed: " + err.Error())
		return
	}
	w.write("info", "settings.json changed on disk — reloaded into runtime")
	if w.bus != nil {
		w.bus.Emit(contracts.EventSettingsApplied, contracts.SettingsAppliedEvent{})
	}
}

func (w *Watcher) reject(reason string) {
	w.write("warn", "external settings change skipped (%s) — previous values stay active", reason)
	if w.bus != nil {
		w.bus.Emit(contracts.EventSettingsRejected, contracts.SettingsRejectedEvent{Reason: reason})
	}
}
