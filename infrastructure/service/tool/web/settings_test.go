package toolweb

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type fixedSettings struct{ st service.WebSearchSettings }

func (f *fixedSettings) WebSearchSettings(context.Context) (service.WebSearchSettings, error) {
	return f.st, nil
}

func searchCall(t *testing.T) entity.ToolCall {
	t.Helper()
	call, err := entity.NewToolCall("c1", "web_search", `{"q":"x"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func TestMasterSwitchHidesAndRefuses(t *testing.T) {
	settings := &fixedSettings{st: defaults}
	tool := toolWithClient(t, stubDef("search", `{"results":[]}`, nil), nil).(*Tool)
	tool.settings = settings
	if !tool.Schema().Enabled() {
		t.Fatal("the tool is hidden while on")
	}
	settings.st.Enabled = false
	if tool.Schema().Enabled() {
		t.Fatal("the tool is offered while off")
	}
	_, err := tool.Execute(context.Background(), searchCall(t), nil)
	if apperror.As(err).StatusCode() != 409 {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchwireBackendIgnoresTheTurnsProvider(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return omniResponse(`{"results":[]}`), nil
	})}
	tool := toolWithClient(t, stubDef("search", `{"results":[]}`, nil), client).(*Tool)
	st := defaults
	st.Backend = service.WebBackendSearchwire
	tool.settings = &fixedSettings{st: st}
	res, err := tool.Execute(omniCtx(), searchCall(t), nil)
	if err != nil || called || !strings.Contains(res.Text, `"backend": "searchwire"`) {
		t.Fatalf("called=%v res=%q err=%v", called, res.Text, err)
	}
}

func TestProviderBackendUsesTheChosenProvider(t *testing.T) {
	var sawAuth string
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		sawAuth = r.Header.Get("Authorization")
		return omniResponse(`{"provider":"x","results":[{"title":"Go","url":"https://go.dev","snippet":"s"}]}`), nil
	})}
	tool := toolWithClient(t, stubDef("search", `{"results":[]}`, nil), client).(*Tool)
	st := defaults
	st.Backend = service.WebBackendProvider
	st.Provider = service.WebProvider{Kind: "9router", Endpoint: "http://r.local/v1", APIKey: "chosen"}
	tool.settings = &fixedSettings{st: st}
	// The turn runs on another provider; the policy names 9router.
	if _, err := tool.Execute(context.Background(), searchCall(t), nil); err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer chosen" {
		t.Fatalf("auth = %q", sawAuth)
	}
}

func TestSourceKeysAreMasked(t *testing.T) {
	st := defaults
	st.Serper = service.WebSearchSource{Enabled: true, Key: "serper-secret"}
	tool := toolWithClient(t, stubDef("search", `{"errors":[{"source":"serper","error":"bad key serper-secret"}]}`, nil), nil).(*Tool)
	tool.settings = &fixedSettings{st: st}
	res, err := tool.Execute(context.Background(), searchCall(t), nil)
	if err != nil || strings.Contains(res.Text, "serper-secret") || !strings.Contains(res.Text, "[api key]") {
		t.Fatalf("res=%q err=%v", res.Text, err)
	}
	failing := toolWithClient(t, stubDef("search", "", errors.New("serper: key serper-secret refused")), nil).(*Tool)
	failing.settings = &fixedSettings{st: st}
	if _, err := failing.Execute(context.Background(), searchCall(t), nil); err == nil || strings.Contains(err.Error(), "serper-secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearcherConfigCarriesTheSettings(t *testing.T) {
	st := service.WebSearchSettings{
		Limit: 20, Timeout: 5 * time.Second, UserAgent: "ua", MaxRawBytes: 1 << 20,
		Brave: service.WebSearchSource{Enabled: false}, Tavily: service.WebSearchSource{Enabled: true, Key: "tv"},
		GitHub: service.WebSearchGitHub{Enabled: true, SearchIssues: false, Token: "gh"},
	}
	c := searcherConfig(st)
	if c.Limit != 20 || c.Timeout != 5*time.Second || c.UserAgent != "ua" || c.MaxRawResponseBytes != 1<<20 ||
		c.MaxResponseBytes != fetchTextBudget || *c.Brave.Enabled || !*c.Tavily.Enabled || c.Tavily.APIKey != "tv" ||
		c.GitHub.Token != "gh" || *c.GitHub.SearchIssues {
		t.Fatalf("config = %+v", c)
	}
}
