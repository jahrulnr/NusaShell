package application

import (
	"context"
	"encoding/json"
	"nusashell/application/settings"
	"nusashell/contracts"
	"nusashell/domain"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- from settings_test.go ---

// TestResolveMaxOutputPrefersModelAdvertisedLimit: when the model advertises
// its own max output, that value wins over the global settings default.
func TestResolveMaxOutputPrefersModelAdvertisedLimit(t *testing.T) {
	provider := &domain.Provider{
		Models: []domain.Model{
			{ID: "gpt-4o", MaxOutput: 16384},
			{ID: "no-limit-model"},
		},
	}
	settings := domain.Settings{MaxOutputTokens: 65536}

	if got := domain.ResolveMaxOutput(provider, "gpt-4o", settings); got != 16384 {
		t.Fatalf("model with advertised limit: got %d, want 16384", got)
	}
	if got := domain.ResolveMaxOutput(provider, "no-limit-model", settings); got != 65536 {
		t.Fatalf("model without advertised limit: got %d, want 65536 (settings default)", got)
	}
	if got := domain.ResolveMaxOutput(provider, "unknown-model", settings); got != 65536 {
		t.Fatalf("unknown model: got %d, want 65536 (settings default)", got)
	}
}

// TestResolveMaxOutputCapsAtSettings: when the model advertises a very high
// max output (e.g. 1M tokens), the settings default acts as a ceiling to
// prevent sending absurdly high max_tokens that cause credit rejections on
// gateways like OpenRouter.
func TestResolveMaxOutputCapsAtSettings(t *testing.T) {
	provider := &domain.Provider{
		Models: []domain.Model{
			{ID: "deepseek-v4-flash", MaxOutput: 1048576},
		},
	}
	settings := domain.Settings{MaxOutputTokens: 65536}

	if got := domain.ResolveMaxOutput(provider, "deepseek-v4-flash", settings); got != 65536 {
		t.Fatalf("model with 1M output should be capped at settings default: got %d, want 65536", got)
	}

	// When the user raises the cap, the model's limit is still respected if lower
	settings.MaxOutputTokens = 2000000
	if got := domain.ResolveMaxOutput(provider, "deepseek-v4-flash", settings); got != 1048576 {
		t.Fatalf("model with 1M output and 2M cap: got %d, want 1048576", got)
	}
}

// TestNormalizeSettingsFillsMaxTokenDefaults: settings written before
// max_input_tokens/max_output_tokens existed get the factory defaults.
func TestNormalizeSettingsFillsMaxTokenDefaults(t *testing.T) {
	old := domain.Settings{
		CompactionEnabled:   true,
		CompactionThreshold: 40000,
		PromptCaching:       true,
		MaxToolRounds:       8,
	}
	normalized := domain.NormalizeSettings(old)
	if normalized.MaxInputTokens != 200000 {
		t.Fatalf("max_input_tokens = %d, want 200000", normalized.MaxInputTokens)
	}
	if normalized.MaxOutputTokens != 65536 {
		t.Fatalf("max_output_tokens = %d, want 65536", normalized.MaxOutputTokens)
	}
	// Old flat default 40000 migrates to 0 (auto = 80% of model context).
	if normalized.CompactionThreshold != 0 {
		t.Fatalf("compaction_threshold = %d, want 0 (auto after migration)", normalized.CompactionThreshold)
	}
}

// TestNormalizeSettingsFillsMaxParallelTools: settings written before
// max_parallel_tools existed get the factory default (6), and out-of-range
// values are clamped into the valid 1–64 band.
func TestNormalizeSettingsFillsMaxParallelTools(t *testing.T) {
	// Unset (0) → default 6.
	zero := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8})
	if zero.MaxParallelTools != 6 {
		t.Fatalf("max_parallel_tools = %d, want 6 (default)", zero.MaxParallelTools)
	}
	// Negative → default 6.
	neg := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, MaxParallelTools: -1})
	if neg.MaxParallelTools != 6 {
		t.Fatalf("max_parallel_tools = %d, want 6 (default for negative)", neg.MaxParallelTools)
	}
	// In-range preserved.
	ok := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, MaxParallelTools: 12})
	if ok.MaxParallelTools != 12 {
		t.Fatalf("max_parallel_tools = %d, want 12 (preserved)", ok.MaxParallelTools)
	}
	// Above cap → clamped to 64.
	over := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, MaxParallelTools: 999})
	if over.MaxParallelTools != 64 {
		t.Fatalf("max_parallel_tools = %d, want 64 (clamped)", over.MaxParallelTools)
	}
}

// TestDefaultSettingsSoundNotificationsOn: the factory default has sound
// notifications enabled so the UI plays turn-complete/error cues without
// requiring the user to opt in.
func TestDefaultSettingsSoundNotificationsOn(t *testing.T) {
	s := domain.DefaultSettings()
	if !s.SoundNotifications {
		t.Fatal("default SoundNotifications = false, want true")
	}
}

// TestNormalizeSettingsPreservesSoundNotifications: NormalizeSettings must
// not reset SoundNotifications to the default when the field is explicitly
// false (user disabled it). Toggles use zero-value=false semantics, so we
// cannot distinguish "unset" from "intentionally false" — but the field is
// omitempty on the wire, so a settings file written by an older version
// simply omits it and NormalizeSettings leaves it as false. The UI treats
// false as "disabled" only when the DTO reports it; the default-on behavior
// is enforced by the frontend (`!== false` check) and by DefaultSettings
// for fresh installs.
func TestNormalizeSettingsPreservesSoundNotifications(t *testing.T) {
	disabled := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, SoundNotifications: false})
	if disabled.SoundNotifications != false {
		t.Fatal("NormalizeSettings should preserve SoundNotifications=false")
	}
	enabled := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, SoundNotifications: true})
	if !enabled.SoundNotifications {
		t.Fatal("NormalizeSettings should preserve SoundNotifications=true")
	}
}

// TestNormalizeSettingsPreservesUserPrompt: NormalizeSettings must not
// clear UserPrompt when it is set. An empty UserPrompt (unset) stays empty.
func TestNormalizeSettingsPreservesUserPrompt(t *testing.T) {
	withPrompt := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8, UserPrompt: "Always respond in Indonesian."})
	if withPrompt.UserPrompt != "Always respond in Indonesian." {
		t.Fatalf("UserPrompt = %q, want preserved", withPrompt.UserPrompt)
	}
	withoutPrompt := domain.NormalizeSettings(domain.Settings{MaxToolRounds: 8})
	if withoutPrompt.UserPrompt != "" {
		t.Fatalf("UserPrompt = %q, want empty", withoutPrompt.UserPrompt)
	}
}

// TestHandleSettingsSetSlowDown: settings.set must persist a valid
// slow_down, reject out-of-range values, and round-trip it through the DTO.
func TestHandleSettingsSetSlowDown(t *testing.T) {
	app := &App{Settings: &memSettingsStore{s: domain.DefaultSettings()}, Logs: &fakeLogStore{}}

	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{SlowDown: intPtr(3)}); rpcErr != nil {
		t.Fatalf("set slow_down=3: %v", rpcErr.Message)
	}
	if got := app.Settings.Get().SlowDown; got != 3 {
		t.Fatalf("SlowDown after set = %d, want 3", got)
	}
	if dto := settingsDTO(app.Settings.Get()); dto.SlowDown != 3 {
		t.Fatalf("settingsDTO.SlowDown = %d, want 3", dto.SlowDown)
	}

	for _, bad := range []int{-1, 61} {
		if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{SlowDown: intPtr(bad)}); rpcErr == nil {
			t.Fatalf("slow_down=%d must be rejected", bad)
		}
	}

	// Clearing back to 0 (off) must work.
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{SlowDown: intPtr(0)}); rpcErr != nil {
		t.Fatalf("set slow_down=0: %v", rpcErr.Message)
	}
	if got := app.Settings.Get().SlowDown; got != 0 {
		t.Fatalf("SlowDown after clear = %d, want 0", got)
	}
}

func TestHandleSettingsSetLearnerNudgeInterval(t *testing.T) {
	app := &App{Settings: &memSettingsStore{s: domain.DefaultSettings()}, Logs: &fakeLogStore{}}

	if dto := settingsDTO(app.Settings.Get()); dto.LearnerNudgeInterval != domain.DefaultLearnerNudgeInterval {
		t.Fatalf("unset DTO = %d, want default %d", dto.LearnerNudgeInterval, domain.DefaultLearnerNudgeInterval)
	}

	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{LearnerNudgeInterval: intPtr(4)}); rpcErr != nil {
		t.Fatalf("set learner_nudge_interval=4: %v", rpcErr.Message)
	}
	if got := domain.EffectiveLearnerNudgeInterval(app.Settings.Get().LearnerNudgeInterval); got != 4 {
		t.Fatalf("stored = %d, want 4", got)
	}
	if dto := settingsDTO(app.Settings.Get()); dto.LearnerNudgeInterval != 4 {
		t.Fatalf("DTO = %d, want 4", dto.LearnerNudgeInterval)
	}

	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{LearnerNudgeInterval: intPtr(0)}); rpcErr != nil {
		t.Fatalf("set learner_nudge_interval=0: %v", rpcErr.Message)
	}
	if dto := settingsDTO(app.Settings.Get()); dto.LearnerNudgeInterval != 0 {
		t.Fatalf("DTO after disable = %d, want 0", dto.LearnerNudgeInterval)
	}

	for _, bad := range []int{-1, domain.LearnerNudgeIntervalCap + 1} {
		if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{LearnerNudgeInterval: intPtr(bad)}); rpcErr == nil {
			t.Fatalf("learner_nudge_interval=%d must be rejected", bad)
		}
	}
}

func TestHandleSettingsSetPetsAutoStart(t *testing.T) {
	app := &App{Settings: &memSettingsStore{s: domain.DefaultSettings()}, Logs: &fakeLogStore{}}

	if dto := settingsDTO(app.Settings.Get()); dto.PetsAutoStart {
		t.Fatal("default PetsAutoStart = true, want false")
	}

	on := true
	got, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{PetsAutoStart: &on})
	if rpcErr != nil {
		t.Fatalf("set pets_auto_start=true: %v", rpcErr.Message)
	}
	if !app.Settings.Get().PetsAutoStart {
		t.Fatal("PetsAutoStart after set = false, want true")
	}
	res, ok := got.(contracts.SettingsGetResult)
	if !ok {
		t.Fatalf("set result type = %T, want SettingsGetResult", got)
	}
	if !res.Settings.PetsAutoStart {
		t.Fatal("settings.set result pets_auto_start = false, want true")
	}
	if dto := settingsDTO(app.Settings.Get()); !dto.PetsAutoStart {
		t.Fatal("settingsDTO.PetsAutoStart = false, want true")
	}

	off := false
	if _, rpcErr := app.handleSettingsSet(contracts.SettingsSetRequest{PetsAutoStart: &off}); rpcErr != nil {
		t.Fatalf("set pets_auto_start=false: %v", rpcErr.Message)
	}
	if app.Settings.Get().PetsAutoStart {
		t.Fatal("PetsAutoStart after clear = true, want false")
	}
}

func TestLearnerNudgeIntervalUsesSettings(t *testing.T) {
	app := &App{}
	if got := app.learnerNudgeInterval(); got != domain.DefaultLearnerNudgeInterval {
		t.Fatalf("nil settings = %d, want default", got)
	}
	zero := 0
	app.Settings = &fakeSettings{Settings: domain.Settings{LearnerNudgeInterval: &zero}}
	if got := app.learnerNudgeInterval(); got != 0 {
		t.Fatalf("disabled = %d, want 0", got)
	}
}

// --- from settings_watcher_test.go ---

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
	w := settings.NewWatcher(settings.WatcherDeps{
		DataDir: dataDir, Store: store, Bus: bus,
		Interval: 30 * time.Millisecond,
	})
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

	time.Sleep(120 * time.Millisecond) // several watcher ticks
	assertNoEvents(t, fx, 80*time.Millisecond)
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
	assertNoEvents(t, fx, 80*time.Millisecond)
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

// --- from project_memory_settings_test.go ---

func TestHandleSettingsSetProjectMemoryBase(t *testing.T) {
	app := NewApp(Deps{Settings: &fakeSettings{Settings: domain.DefaultSettings()}})

	rel := "relative/path"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &rel}); err == nil {
		t.Fatal("relative project_memory_base must fail validation")
	} else if !strings.Contains(err.Message, "absolute") {
		t.Fatalf("validation message = %s", err.Message)
	}

	empty := ""
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &empty}); err != nil {
		t.Fatalf("empty base must be allowed: %v", err)
	}
	if app.Settings.Get().ProjectMemoryBase != "" {
		t.Fatalf("empty should clear the setting, got %q", app.Settings.Get().ProjectMemoryBase)
	}

	abs := t.TempDir()
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &abs}); err != nil {
		t.Fatalf("absolute base: %v", err)
	}
	if got := app.Settings.Get().ProjectMemoryBase; got != filepath.Clean(abs) {
		t.Fatalf("stored base = %q, want %q", got, filepath.Clean(abs))
	}

	home := osUserHomeForTest(t)
	tilde := "~/.memory"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{ProjectMemoryBase: &tilde}); err != nil {
		t.Fatalf("~ expansion: %v", err)
	}
	want := filepath.Join(home, ".memory")
	if got := app.Settings.Get().ProjectMemoryBase; got != want {
		t.Fatalf("expanded base = %q, want %q", got, want)
	}
}

func osUserHomeForTest(t *testing.T) string {
	t.Helper()
	home, err := domain.ExpandHomeDir("~")
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// --- from websearch_settings_test.go ---

// webSearchFakeCreds is a CredentialStore with real Set/Delete for the
// settings roundtrip tests (the shared fakeCreds no-ops mutations).
type webSearchFakeCreds struct {
	keys map[string]string
}

func (c *webSearchFakeCreds) Get(id string) (string, bool, error) {
	k, ok := c.keys[id]
	return k, ok, nil
}
func (c *webSearchFakeCreds) Set(id, key string) error { c.keys[id] = key; return nil }
func (c *webSearchFakeCreds) Delete(id string) error   { delete(c.keys, id); return nil }
func (c *webSearchFakeCreds) ListByPrefix(p string) ([]string, error) {
	out := []string{}
	for id := range c.keys {
		if strings.HasPrefix(id, p) {
			out = append(out, id)
		}
	}
	return out, nil
}

func TestHandleSettingsSetWebSearchStrategy(t *testing.T) {
	app := NewApp(Deps{Settings: &fakeSettings{}})

	bad := "all-at-once"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{WebSearchStrategy: &bad}); err == nil {
		t.Fatal("invalid strategy must fail validation")
	}

	for _, ok := range []string{"", "auto", "round_robin", "random", "brave", "serper", "tavily", "startpage", "wikipedia", "github"} {
		v := ok
		if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{WebSearchStrategy: &v}); err != nil {
			t.Fatalf("strategy %q must be accepted: %v", v, err)
		}
		if got := app.Settings.Get().WebSearchStrategy; got != v {
			t.Fatalf("stored strategy = %q, want %q", got, v)
		}
	}

	// The value round-trips through settings.get untainted (no key fields).
	result, rpcErr := app.handleSettingsGet()
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if got := result.(contracts.SettingsGetResult).Settings.WebSearchStrategy; got != "github" {
		t.Fatalf("DTO strategy = %q, want github", got)
	}
}

func TestHandleSettingsSetWebSearchAPIKeys(t *testing.T) {
	app := NewApp(Deps{
		Settings:    &fakeSettings{},
		Credentials: &webSearchFakeCreds{keys: map[string]string{}},
	})

	brave := "brave-secret"
	serper := "serper-secret"
	tavily := "tavily-secret"
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{
		WebSearchBraveAPIKey:  &brave,
		WebSearchSerperAPIKey: &serper,
		WebSearchTavilyAPIKey: &tavily,
	}); err != nil {
		t.Fatal(err)
	}
	fc := app.Credentials.(*webSearchFakeCreds)
	if fc.keys["web_search_brave"] != "brave-secret" ||
		fc.keys["web_search_serper"] != "serper-secret" ||
		fc.keys["web_search_tavily"] != "tavily-secret" {
		t.Fatalf("stored keys = %#v", fc.keys)
	}
	// Keys must never leak into settings.json / settings.get.
	if strings.Contains(mustMarshalSettings(t, app), "brave-secret") {
		t.Fatal("API key leaked into stored settings")
	}

	// Empty value clears the stored key (env fallback takes over).
	empty := ""
	if _, err := app.handleSettingsSet(contracts.SettingsSetRequest{WebSearchSerperAPIKey: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fc.keys["web_search_serper"]; ok {
		t.Fatal("empty key must delete the stored credential")
	}
}

func mustMarshalSettings(t *testing.T, app *App) string {
	t.Helper()
	data, err := json.Marshal(app.Settings.Get())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
