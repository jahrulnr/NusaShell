package appwebsearch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/application/setting"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoconfig "github.com/jahrulnr/nusashell/infrastructure/repository/config"
)

type vault map[string]string

func (v vault) Get(_ context.Context, k string) (string, error) {
	if s, ok := v[k]; ok {
		return s, nil
	}
	return "", apperror.New(404, "not found", nil)
}
func (v vault) Set(_ context.Context, k, s string) error { v[k] = s; return nil }
func (v vault) Delete(_ context.Context, k string) error {
	if _, ok := v[k]; !ok {
		return apperror.New(404, "not found", nil)
	}
	delete(v, k)
	return nil
}
func (v vault) Close() error { return nil }

var _ drepo.SecretStore = vault{}

func newApp(t *testing.T) (*App, vault, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(config.NewConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	secrets := vault{}
	app := New(Deps{
		Config: func() (config.WebSearchConfig, error) {
			c := config.NewConfig()
			if err := c.Load(repoconfig.NewLocal(), path); err != nil {
				return config.WebSearchConfig{}, err
			}
			return c.WebSearch, nil
		},
		Settings: setting.New(repoconfig.NewLocal(), path),
		Secrets:  secrets,
		Native:   func(id string) (bool, bool) { return id == "omniroute", false },
		Defaults: Defaults{Limit: 10, Timeout: 10 * time.Second},
	})
	return app, secrets, path
}

func sourceIn(t *testing.T, st Status, id string) SourceView {
	t.Helper()
	for _, s := range st.Sources {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no source %q in %+v", id, st.Sources)
	return SourceView{}
}

func TestDefaultsWithoutKeys(t *testing.T) {
	for _, env := range []string{"BRAVE_SEARCH_API_KEY", "SERPER_API_KEY", "TAVILY_API_KEY", "GITHUB_TOKEN"} {
		t.Setenv(env, "")
	}
	app, _, _ := newApp(t)
	st, err := app.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.Backend != "auto" || st.Limit != 0 || st.Defaults.Limit != 10 {
		t.Fatalf("status = %+v", st)
	}
	brave, serper := sourceIn(t, st, "brave"), sourceIn(t, st, "serper")
	if !brave.Active || brave.KeyState != "unset" || serper.Enabled || serper.Active || !serper.NeedsKey {
		t.Fatalf("brave=%+v serper=%+v", brave, serper)
	}
	if sp := sourceIn(t, st, "startpage"); !sp.Fixed || !sp.Active {
		t.Fatalf("startpage = %+v", sp)
	}
}

func TestSetKeyVaultsAndNeverAnswersIt(t *testing.T) {
	t.Setenv("SERPER_API_KEY", "")
	app, secrets, path := newApp(t)
	ctx := context.Background()
	st, err := app.SetKey(ctx, "serper", "  sk-serper-123  ")
	if err != nil {
		t.Fatal(err)
	}
	serper := sourceIn(t, st, "serper")
	if serper.KeyState != "vaulted" || !serper.Enabled || !serper.Active {
		t.Fatalf("serper = %+v", serper)
	}
	if secrets[drepo.WebSearchSecretKey("serper")] != "sk-serper-123" {
		t.Fatalf("vault = %v", secrets)
	}
	file, _ := os.ReadFile(path)
	if strings.Contains(string(file), "sk-serper-123") ||
		!strings.Contains(string(file), valueobject.SecretRef(drepo.WebSearchSecretKey("serper"))) {
		t.Fatalf("config file = %s", file)
	}
	out, _ := json.Marshal(st)
	if strings.Contains(string(out), "sk-serper-123") {
		t.Fatalf("status leaks the key: %s", out)
	}
	resolved, err := app.WebSearchSettings(ctx)
	if err != nil || resolved.Serper.Key != "sk-serper-123" || !resolved.Serper.Enabled {
		t.Fatalf("resolved = %+v %v", resolved.Serper, err)
	}

	st, err = app.ClearKey(ctx, "serper")
	if err != nil {
		t.Fatal(err)
	}
	if s := sourceIn(t, st, "serper"); s.KeyState != "unset" || s.Active {
		t.Fatalf("after clear = %+v", s)
	}
	if _, ok := secrets[drepo.WebSearchSecretKey("serper")]; ok {
		t.Fatal("the vault still holds the key")
	}
	// Clearing again is fine: nothing is left to forget.
	if _, err := app.ClearKey(ctx, "serper"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvReference(t *testing.T) {
	t.Setenv("MY_BRAVE", "brave-from-env")
	app, secrets, _ := newApp(t)
	st, err := app.SetKey(context.Background(), "brave", "env:MY_BRAVE")
	if err != nil {
		t.Fatal(err)
	}
	if b := sourceIn(t, st, "brave"); b.KeyState != "env" || len(secrets) != 0 {
		t.Fatalf("brave = %+v vault = %v", b, secrets)
	}
	resolved, _ := app.WebSearchSettings(context.Background())
	if resolved.Brave.Key != "brave-from-env" {
		t.Fatalf("key = %q", resolved.Brave.Key)
	}
	t.Setenv("MY_BRAVE", "")
	st, _ = app.Status(context.Background())
	if b := sourceIn(t, st, "brave"); b.KeyState != "missing" || b.Note == "" {
		t.Fatalf("brave without its variable = %+v", b)
	}
}

func TestDanglingVaultReferenceReadsAsMissing(t *testing.T) {
	app, secrets, _ := newApp(t)
	ctx := context.Background()
	if _, err := app.SetKey(ctx, "tavily", "tv-1"); err != nil {
		t.Fatal(err)
	}
	delete(secrets, drepo.WebSearchSecretKey("tavily"))
	app.forget()
	st, _ := app.Status(ctx)
	if tv := sourceIn(t, st, "tavily"); tv.KeyState != "missing" || tv.Active {
		t.Fatalf("tavily = %+v", tv)
	}
	resolved, err := app.WebSearchSettings(ctx)
	if err != nil || resolved.Tavily.Key != "" {
		t.Fatalf("resolved = %+v %v", resolved.Tavily, err)
	}
}

func TestLiteralReferenceRefused(t *testing.T) {
	app, _, _ := newApp(t)
	if _, err := app.d.Settings.SetMany(map[string]string{"websearch.brave.key_ref": "plain-key"}); err == nil {
		t.Fatal("a literal key in the config was accepted")
	}
	if _, err := app.SetKey(context.Background(), "brave", "secret:other"); err == nil {
		t.Fatal("a vault reference was accepted as a key")
	}
	if _, err := app.SetKey(context.Background(), "bing", "k"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("unknown source: %v", err)
	}
}

func TestUpdateIsLiveAndWhitelisted(t *testing.T) {
	app, _, _ := newApp(t)
	ctx := context.Background()
	if _, err := app.WebSearchSettings(ctx); err != nil { // warm the cache
		t.Fatal(err)
	}
	st, err := app.Update(ctx, map[string]json.RawMessage{"limit": json.RawMessage(`20`), "enabled": json.RawMessage(`"off"`)})
	if err != nil {
		t.Fatal(err)
	}
	if st.Limit != 20 || st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	resolved, _ := app.WebSearchSettings(ctx)
	if resolved.Limit != 20 || resolved.Enabled {
		t.Fatalf("resolved = %+v", resolved)
	}
	if _, err := app.Update(ctx, map[string]json.RawMessage{"brave.key_ref": json.RawMessage(`"env:X"`)}); err == nil {
		t.Fatal("a key reference was set through Update")
	}
	if _, err := app.Update(ctx, map[string]json.RawMessage{"limit": json.RawMessage(`99`)}); err == nil {
		t.Fatal("an out-of-range limit was accepted")
	}
}

func TestEditsMadeElsewhereArriveAfterTheCache(t *testing.T) {
	app, _, _ := newApp(t)
	clock := time.Now()
	app.now = func() time.Time { return clock }
	ctx := context.Background()
	if _, err := app.WebSearchSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := app.d.Settings.SetMany(map[string]string{"websearch.limit": "7"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := app.WebSearchSettings(ctx); st.Limit != 0 {
		t.Fatalf("within the cache window limit = %d", st.Limit)
	}
	clock = clock.Add(cacheFor)
	if st, _ := app.WebSearchSettings(ctx); st.Limit != 7 {
		t.Fatalf("after the cache window limit = %d", st.Limit)
	}
}

func TestProviderBackendProblem(t *testing.T) {
	app, _, _ := newApp(t)
	st, err := app.Update(context.Background(), map[string]json.RawMessage{
		"backend": json.RawMessage(`"provider"`), "provider": json.RawMessage(`"openai"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.ProviderProblem == "" {
		t.Fatal("a provider without native search was not flagged")
	}
}

type stubTool struct {
	name string
	text string
	err  error
	got  string
}

func (s *stubTool) Schema() valueobject.ToolSchema {
	sch, _ := valueobject.NewToolSchema(s.name, "stub", json.RawMessage(`{"type":"object"}`), true, enum.ToolPermissionAgent)
	return sch
}
func (s *stubTool) Execute(_ context.Context, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	s.got = call.Input()
	return dservice.ToolResult{Text: s.text}, s.err
}

func TestRunsTheRealTool(t *testing.T) {
	app, _, _ := newApp(t)
	search := &stubTool{name: "web_search", text: `{"results":[{"title":"x"}],"backend":"searchwire"}`}
	fetch := &stubTool{name: "web_fetch", err: apperror.New(502, "brave: 429 too many requests", nil)}
	app.UseTools(search, fetch)
	ctx := context.Background()

	res, err := app.Test(ctx, TestInput{Tool: "search", Query: "golang", Limit: 3})
	if err != nil || !strings.Contains(string(res.Output), `"title"`) {
		t.Fatalf("search = %+v %v", res, err)
	}
	if !strings.Contains(search.got, `"query":"golang"`) || !strings.Contains(search.got, `"limit":3`) {
		t.Fatalf("search input = %s", search.got)
	}
	res, err = app.Test(ctx, TestInput{Tool: "fetch", URL: "https://example.com"})
	if err != nil || res.Error != "brave: 429 too many requests" {
		t.Fatalf("fetch = %+v %v", res, err)
	}
	if _, err := app.Test(ctx, TestInput{Tool: "search"}); err == nil {
		t.Fatal("an empty query was run")
	}
	if _, err := app.Test(ctx, TestInput{Tool: "answer", Query: "x"}); err == nil {
		t.Fatal("an unknown tool was run")
	}
}
