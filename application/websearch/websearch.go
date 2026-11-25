// Package appwebsearch owns the web tools' settings: what the owner turned
// on, where each source's key lives, which backend serves the tools, and
// a test that runs the real tools. Behavior lives in the config file;
// keys live in the vault (or the environment) and the file only carries
// their reference. The tools read the resolved settings on every call.
package appwebsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// cacheFor bounds how stale resolved settings may be after an edit made
// outside this app (`nusashell config set`); the app's own writes drop
// the cache at once.
const cacheFor = 2 * time.Second

// toolCallDeadline caps the test-run's tool call — a variable so a test
// can prove the seam without sleeping four minutes. Production keeps the
// domain ceiling; nothing else may set it.
var toolCallDeadline = dservice.ToolCallDeadline

// Settings writes the config file: SettingApp satisfies it.
type Settings interface {
	SetMany(values map[string]string) ([]byte, error)
	Unset(key string) ([]byte, error)
}

// Defaults are the numbers a zero setting stands for, for display.
type Defaults struct {
	Limit            int
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRawBytes      int64
	UserAgent        string
}

// Deps wires the use case.
type Deps struct {
	// Config reads the current websearch section.
	Config   func() (config.WebSearchConfig, error)
	Settings Settings
	Secrets  drepo.SecretStore
	// Providers resolves the provider the "provider" backend names.
	Providers drepo.Provider
	// Native reports whether a provider id can serve web search itself,
	// and whether only through a chosen model.
	Native   func(providerID string) (native, needsModel bool)
	Defaults Defaults
	Log      *slog.Logger
}

type App struct {
	d Deps
	// search and fetch are the real tools, for Test. They read their
	// settings from this app, so they arrive after it is built.
	search, fetch dservice.Tool

	mu       sync.Mutex
	cached   dservice.WebSearchSettings
	cachedAt time.Time
	now      func() time.Time
}

func New(d Deps) *App {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &App{d: d, now: time.Now}
}

var _ dservice.WebSearchConfigurer = (*App)(nil)

// UseTools hands over the web tools Test runs. Call it once, before
// serving.
func (a *App) UseTools(search, fetch dservice.Tool) { a.search, a.fetch = search, fetch }

// source is one keyed search source as the config and the vault know it.
type source struct {
	id, label, env string
	needsKey       bool
	cfg            func(config.WebSearchConfig) (enabled, ref string)
	leaf           string // the config key of its reference
}

var sources = []source{
	{id: "brave", label: "Brave", env: "BRAVE_SEARCH_API_KEY", leaf: "websearch.brave.key_ref",
		cfg: func(c config.WebSearchConfig) (string, string) { return c.Brave.Enabled, c.Brave.KeyRef }},
	{id: "serper", label: "Serper (Google)", env: "SERPER_API_KEY", needsKey: true, leaf: "websearch.serper.key_ref",
		cfg: func(c config.WebSearchConfig) (string, string) { return c.Serper.Enabled, c.Serper.KeyRef }},
	{id: "tavily", label: "Tavily", env: "TAVILY_API_KEY", needsKey: true, leaf: "websearch.tavily.key_ref",
		cfg: func(c config.WebSearchConfig) (string, string) { return c.Tavily.Enabled, c.Tavily.KeyRef }},
	{id: "github", label: "GitHub", env: "GITHUB_TOKEN", leaf: "websearch.github.token_ref",
		cfg: func(c config.WebSearchConfig) (string, string) { return c.GitHub.Enabled, c.GitHub.TokenRef }},
}

func sourceByID(id string) (source, bool) {
	for _, s := range sources {
		if s.id == id {
			return s, true
		}
	}
	return source{}, false
}

// key resolves a reference: "env:NAME" reads the environment, a vault
// reference reads the vault, and no reference lets the library read its
// own variable (answered here as that variable's value, for status).
// A vault entry that is gone reads as no key; the status says so.
func (a *App) key(ctx context.Context, s source, ref string) (value, state string) {
	switch {
	case ref == "":
		if v := os.Getenv(s.env); v != "" {
			return "", "env" // the library reads it itself
		}
		return "", "unset"
	case strings.HasPrefix(ref, valueobject.KeyRefEnv):
		if v := os.Getenv(strings.TrimPrefix(ref, valueobject.KeyRefEnv)); v != "" {
			return v, "env"
		}
		return "", "missing"
	case strings.HasPrefix(ref, valueobject.KeyRefSecret):
		if a.d.Secrets == nil {
			return "", "missing"
		}
		v, err := a.d.Secrets.Get(ctx, strings.TrimPrefix(ref, valueobject.KeyRefSecret))
		if err != nil {
			if apperror.As(err).StatusCode() != 404 {
				a.d.Log.Warn("websearch: could not read a key from the vault", "source", s.id, "err", err)
			}
			return "", "missing"
		}
		return v, "vaulted"
	}
	return "", "missing"
}

// WebSearchSettings answers the resolved settings the tools run on.
func (a *App) WebSearchSettings(ctx context.Context) (dservice.WebSearchSettings, error) {
	a.mu.Lock()
	if !a.cachedAt.IsZero() && a.now().Sub(a.cachedAt) < cacheFor {
		st := a.cached
		a.mu.Unlock()
		return st, nil
	}
	a.mu.Unlock()
	c, err := a.d.Config()
	if err != nil {
		return dservice.WebSearchSettings{}, err
	}
	keys := map[string]string{}
	for _, s := range sources {
		_, ref := s.cfg(c)
		keys[s.id], _ = a.key(ctx, s, ref)
	}
	st := dservice.WebSearchSettings{
		Enabled:          c.On(),
		Backend:          dservice.WebBackend(c.Backend),
		Limit:            c.Limit,
		Timeout:          time.Duration(c.Timeout),
		MaxResponseBytes: c.MaxResponseBytes,
		MaxRawBytes:      c.MaxRawBytes,
		UserAgent:        c.UserAgent,
		Brave:            dservice.WebSearchSource{Enabled: c.Brave.Enabled == "on", Key: keys["brave"]},
		Serper:           dservice.WebSearchSource{Enabled: c.Serper.Enabled == "on", Key: keys["serper"]},
		Tavily:           dservice.WebSearchSource{Enabled: c.Tavily.Enabled == "on", Key: keys["tavily"]},
		GitHub: dservice.WebSearchGitHub{Enabled: c.GitHub.Enabled == "on", SearchIssues: c.GitHub.SearchIssues == "on",
			Token: keys["github"]},
	}
	if st.Backend == dservice.WebBackendProvider && a.d.Providers != nil {
		p, err := a.d.Providers.Load(ctx, c.Provider)
		if err != nil {
			// The tools fall back to the metasearch; the status shows why.
			a.d.Log.Warn("websearch: the chosen provider cannot be used", "provider", c.Provider, "err", err)
		} else {
			st.Provider = dservice.WebProviderOf(p, c.Model)
		}
	}
	a.mu.Lock()
	a.cached, a.cachedAt = st, a.now()
	a.mu.Unlock()
	return st, nil
}

func (a *App) forget() {
	a.mu.Lock()
	a.cachedAt = time.Time{}
	a.mu.Unlock()
}

// ---- status ----

// SourceView is one search source as the owner sees it.
type SourceView struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	// Fixed sources are always on: the library has no switch for them.
	Fixed bool `json:"fixed,omitempty"`
	// Keyed sources take a key; NeedsKey ones do nothing without one.
	Keyed    bool   `json:"keyed,omitempty"`
	NeedsKey bool   `json:"needsKey,omitempty"`
	KeyState string `json:"keyState,omitempty"` // unset, env, vaulted, missing
	// Saved says the config names a key for it; without one the library
	// reads KeyEnv itself.
	Saved  bool   `json:"saved,omitempty"`
	KeyEnv string `json:"keyEnv,omitempty"`
	// Active says whether the source takes part in a search now.
	Active bool   `json:"active"`
	Note   string `json:"note,omitempty"`
}

// Status is the web tools' settings, effective values included.
type Status struct {
	Enabled          bool         `json:"enabled"`
	Backend          string       `json:"backend"`
	Provider         string       `json:"provider"`
	Model            string       `json:"model"`
	ProviderProblem  string       `json:"providerProblem,omitempty"`
	Limit            int          `json:"limit"`
	Timeout          string       `json:"timeout"`
	MaxResponseBytes int64        `json:"maxResponseBytes"`
	MaxRawBytes      int64        `json:"maxRawBytes"`
	UserAgent        string       `json:"userAgent"`
	SearchIssues     bool         `json:"searchIssues"`
	Sources          []SourceView `json:"sources"`
	Defaults         DefaultsView `json:"defaults"`
	// Providers are the configured providers, for the backend picker.
	Providers []ProviderOption `json:"providers"`
}

// ProviderOption is a provider the "provider" backend could name.
type ProviderOption struct {
	ID         string `json:"id"`
	Native     bool   `json:"native"`
	NeedsModel bool   `json:"needsModel,omitempty"`
}

type DefaultsView struct {
	Limit            int    `json:"limit"`
	Timeout          string `json:"timeout"`
	MaxResponseBytes int64  `json:"maxResponseBytes"`
	MaxRawBytes      int64  `json:"maxRawBytes"`
	UserAgent        string `json:"userAgent"`
}

// Status answers the settings for Settings → Web search. Key values never
// leave; only where each lives.
func (a *App) Status(ctx context.Context) (Status, error) {
	c, err := a.d.Config()
	if err != nil {
		return Status{}, err
	}
	timeout := ""
	if c.Timeout != 0 {
		timeout = time.Duration(c.Timeout).String()
	}
	out := Status{
		Enabled: c.On(), Backend: c.Backend, Provider: c.Provider, Model: c.Model,
		Limit: c.Limit, Timeout: timeout, MaxResponseBytes: c.MaxResponseBytes, MaxRawBytes: c.MaxRawBytes,
		UserAgent: c.UserAgent, SearchIssues: c.GitHub.SearchIssues == "on",
		Defaults: DefaultsView{
			Limit: a.d.Defaults.Limit, Timeout: a.d.Defaults.Timeout.String(), MaxResponseBytes: a.d.Defaults.MaxResponseBytes,
			MaxRawBytes: a.d.Defaults.MaxRawBytes, UserAgent: a.d.Defaults.UserAgent,
		},
	}
	for _, s := range sources {
		enabled, ref := s.cfg(c)
		v, state := a.key(ctx, s, ref)
		has := v != "" || state == "env"
		view := SourceView{ID: s.id, Label: s.label, Enabled: enabled == "on", Keyed: true, NeedsKey: s.needsKey,
			KeyState: state, KeyEnv: s.env, Saved: ref != ""}
		view.Active = view.Enabled && (!s.needsKey || has)
		switch {
		case state == "missing":
			view.Note = "The saved key is gone — set it again."
		case s.id == "brave" && !has:
			view.Note = "No key: reads Brave's public results page, which can rate-limit."
		case s.id == "brave":
			view.Note = "Uses the Brave Search API."
		case s.id == "github" && !has:
			view.Note = "No token: GitHub's anonymous rate limit applies."
		}
		out.Sources = append(out.Sources, view)
	}
	out.Sources = append(out.Sources,
		SourceView{ID: "startpage", Label: "Startpage", Enabled: true, Fixed: true, Active: true},
		SourceView{ID: "wikipedia", Label: "Wikipedia", Enabled: true, Fixed: true, Active: true})
	out.Providers = []ProviderOption{}
	if a.d.Providers != nil {
		list, err := a.d.Providers.InspectList(ctx)
		if err != nil {
			return Status{}, err
		}
		for _, snap := range list {
			opt := ProviderOption{ID: snap.ID}
			if a.d.Native != nil {
				opt.Native, opt.NeedsModel = a.d.Native(snap.ID)
			}
			out.Providers = append(out.Providers, opt)
		}
	}
	if c.Backend == string(dservice.WebBackendProvider) {
		native, needsModel := false, false
		if a.d.Native != nil {
			native, needsModel = a.d.Native(c.Provider)
		}
		switch {
		case !native:
			out.ProviderProblem = "This provider has no web search of its own; searches use the built-in metasearch."
		case needsModel && c.Model == "":
			out.ProviderProblem = "This provider searches through a model — choose one, or searches use the built-in metasearch."
		case a.d.Providers != nil:
			if _, err := a.d.Providers.Load(ctx, c.Provider); err != nil {
				out.ProviderProblem = "This provider cannot be used: " + apperror.As(err).Error()
			}
		}
	}
	return out, nil
}

// ---- edits ----

// editable are the behavior keys the owner may set here, relative to
// "websearch.". Keys go through SetKey.
var editable = map[string]bool{
	"enabled": true, "backend": true, "provider": true, "model": true, "limit": true, "timeout": true,
	"max_response_bytes": true, "max_raw_bytes": true, "user_agent": true,
	"brave.enabled": true, "serper.enabled": true, "tavily.enabled": true,
	"github.enabled": true, "github.search_issues": true,
}

// Update writes behavior settings as one change — all land or none do.
func (a *App) Update(ctx context.Context, values map[string]json.RawMessage) (Status, error) {
	if len(values) == 0 {
		return Status{}, apperror.New(400, "nothing to change", nil)
	}
	set := make(map[string]string, len(values))
	for k, v := range values {
		if !editable[k] {
			return Status{}, apperror.New(400, fmt.Sprintf("websearch.%s cannot be set here", k), nil)
		}
		set["websearch."+k] = string(v)
	}
	if _, err := a.d.Settings.SetMany(set); err != nil {
		return Status{}, err
	}
	a.forget()
	return a.Status(ctx)
}

// SetKey stores a source's key: an "env:NAME" reference as given, any
// other value in the vault, with the config pointing at it. A source that
// does nothing without a key is turned on with it.
func (a *App) SetKey(ctx context.Context, id, value string) (Status, error) {
	s, ok := sourceByID(id)
	if !ok {
		return Status{}, apperror.New(404, "unknown search source", nil)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return Status{}, apperror.New(400, "the key is empty — use Remove to clear it", nil)
	}
	ref := value
	if !strings.HasPrefix(value, valueobject.KeyRefEnv) {
		if strings.HasPrefix(value, valueobject.KeyRefSecret) {
			return Status{}, apperror.New(400, "paste the key itself, or env:NAME", nil)
		}
		if a.d.Secrets == nil {
			return Status{}, apperror.New(409, "no vault is configured to keep the key — use env:NAME", nil)
		}
		name := drepo.WebSearchSecretKey(s.id)
		if err := a.d.Secrets.Set(ctx, name, value); err != nil {
			return Status{}, err
		}
		ref = valueobject.SecretRef(name)
	}
	set := map[string]string{s.leaf: ref}
	if s.needsKey {
		set["websearch."+s.id+".enabled"] = "on"
	}
	if _, err := a.d.Settings.SetMany(set); err != nil {
		return Status{}, err
	}
	a.forget()
	return a.Status(ctx)
}

// ClearKey forgets a source's key: the reference leaves the config and a
// vaulted value leaves the vault.
func (a *App) ClearKey(ctx context.Context, id string) (Status, error) {
	s, ok := sourceByID(id)
	if !ok {
		return Status{}, apperror.New(404, "unknown search source", nil)
	}
	if _, err := a.d.Settings.Unset(s.leaf); err != nil {
		return Status{}, err
	}
	if a.d.Secrets != nil {
		if err := a.d.Secrets.Delete(ctx, drepo.WebSearchSecretKey(s.id)); err != nil && apperror.As(err).StatusCode() != 404 {
			return Status{}, err
		}
	}
	a.forget()
	return a.Status(ctx)
}

// ---- test ----

// TestInput asks for one real search or fetch.
type TestInput struct {
	Tool     string `json:"tool"` // "search" or "fetch"
	Query    string `json:"query"`
	Limit    int    `json:"limit"`
	URL      string `json:"url"`
	MaxBytes int64  `json:"maxBytes"`
}

// TestResult is the tool's own document — what a model would read — and
// how long it took.
type TestResult struct {
	Output     json.RawMessage `json:"output,omitempty"`
	Text       string          `json:"text,omitempty"`
	Error      string          `json:"error,omitempty"`
	DurationMS int64           `json:"durationMs"`
}

// Test runs the real tool, so keys, limits, the backend, and redaction
// are exactly what a thread gets. A failed call is an answer, not an
// error: the owner is testing for failures.
func (a *App) Test(ctx context.Context, in TestInput) (TestResult, error) {
	var (
		tool  dservice.Tool
		input any
	)
	switch in.Tool {
	case "search":
		if strings.TrimSpace(in.Query) == "" {
			return TestResult{}, apperror.New(400, "type something to search for", nil)
		}
		tool, input = a.search, map[string]any{"query": in.Query, "limit": in.Limit}
	case "fetch":
		if strings.TrimSpace(in.URL) == "" {
			return TestResult{}, apperror.New(400, "give a URL to fetch", nil)
		}
		tool, input = a.fetch, map[string]any{"url": in.URL, "max_bytes": in.MaxBytes}
	default:
		return TestResult{}, apperror.New(400, "tool must be search or fetch", nil)
	}
	if tool == nil {
		return TestResult{}, apperror.New(409, "that tool is not available", nil)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return TestResult{}, err
	}
	id, err := valueobject.NewID("call", "")
	if err != nil {
		return TestResult{}, err
	}
	call, err := entity.NewToolCall(id.String(), tool.Schema().Name(), string(raw), enum.TooltypeOrigin)
	if err != nil {
		return TestResult{}, err
	}
	a.forget() // test what was just saved
	start := a.now()
	// The one ceiling covers the test-run too — a hanging endpoint must
	// not park the settings page. Its deadline is the run's own outcome:
	// the shared timed-out text in the response body, never the parent
	// cancel path.
	deadlineCtx, deadlineStop := context.WithTimeout(ctx, toolCallDeadline)
	res, err := tool.Execute(deadlineCtx, call, nil)
	deadlineStop()
	out := TestResult{DurationMS: a.now().Sub(start).Milliseconds()}
	if err != nil {
		// The tool masks keys; its message names the failing sources.
		if deadlineCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			out.Error = dservice.ToolCallTimeoutText
		} else {
			out.Error = err.Error()
		}
		return out, nil
	}
	if json.Valid([]byte(res.Text)) {
		out.Output = json.RawMessage(res.Text)
	} else {
		out.Text = res.Text
	}
	return out, nil
}
