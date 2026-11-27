package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	appagent "github.com/jahrulnr/nusashell/application/agent"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
)

// listingRuntime is the probe the old model route started. A read of the
// stored catalog must not call it.
type listingRuntime struct {
	dservice.AgentRuntime
	calls int
}

func (l *listingRuntime) Reload(string) {}

func (l *listingRuntime) Models(context.Context, *aggregate.Agent) ([]dservice.AgentModel, error) {
	l.calls++
	return []dservice.AgentModel{}, nil
}

func TestAgentModelListDoesNotRenderUnknownAsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &listingRuntime{}
	app := appagent.New(repoagent.New(t.TempDir(), nil), rt, nil, nil)
	if _, err := app.Upsert(context.Background(), appagent.Input{ID: "cursor", Name: "Cursor", Command: "cursor-agent"}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	Register(r.Group("/api"), app)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents/cursor/models", nil))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, body)
	}
	if strings.Contains(body, `"data"`) {
		t.Fatalf("unknown rendered as a list: %s", body)
	}
	if !strings.Contains(body, `"status":"unknown"`) || !strings.Contains(body, `"by":"agent"`) || !strings.Contains(body, "the agent reported no model list") {
		t.Fatalf("body = %s", body)
	}
	if rt.calls != 0 {
		t.Fatalf("the list started the agent %d times", rt.calls)
	}
}

func TestAgentModelListKeepsAKnownCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := repoagent.New(t.TempDir(), nil)
	rt := &listingRuntime{}
	app := appagent.New(repo, rt, nil, nil)
	if _, err := app.Upsert(context.Background(), appagent.Input{ID: "cursor", Name: "Cursor", Command: "cursor-agent"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveCatalog(context.Background(), "cursor", storedCatalog(true, "grok-4.7")); err != nil {
		t.Fatal(err)
	}
	body := getModels(t, app)
	if !strings.Contains(body, `"status":"known"`) || !strings.Contains(body, `"id":"grok-4.7"`) || strings.Contains(body, `"status":"unknown"`) {
		t.Fatalf("known = %s", body)
	}
	if rt.calls != 0 {
		t.Fatalf("known catalog started the agent %d times", rt.calls)
	}

	if err := repo.SaveCatalog(context.Background(), "cursor", storedCatalog(true)); err != nil {
		t.Fatal(err)
	}
	body = getModels(t, app)
	if !strings.Contains(body, `"status":"known"`) || !strings.Contains(body, `"data":[]`) {
		t.Fatalf("known empty = %s", body)
	}
	if rt.calls != 0 {
		t.Fatalf("known empty started the agent")
	}

	// A stored list from before the reported flag is still a catalog.
	legacy := storedCatalog(false, "grok-4.6")
	if err := repo.SaveCatalog(context.Background(), "cursor", legacy); err != nil {
		t.Fatal(err)
	}
	body = getModels(t, app)
	if !strings.Contains(body, `"status":"known"`) || !strings.Contains(body, `"id":"grok-4.6"`) || strings.Contains(body, `"data":[]`) {
		t.Fatalf("stored models = %s", body)
	}

	// Options were seen, and a current id, but no model list. That stays
	// unknown and keeps the id the agent named.
	if err := repo.SaveCatalog(context.Background(), "cursor", drepo.AgentCatalog{
		CurrentModel: "grok-4.7", ProbedAt: time.Now(),
		Options: []dservice.AgentOption{{ID: "fast", Name: "Fast", Type: "boolean"}},
	}); err != nil {
		t.Fatal(err)
	}
	body = getModels(t, app)
	if strings.Contains(body, `"data"`) || !strings.Contains(body, `"status":"unknown"`) || !strings.Contains(body, `"id":"grok-4.7"`) {
		t.Fatalf("named default rendered as a list: %s", body)
	}
	if rt.calls != 0 {
		t.Fatalf("reading a stored catalog started the agent %d times", rt.calls)
	}
}

func getModels(t *testing.T, app *appagent.AgentApp) string {
	t.Helper()
	r := gin.New()
	Register(r.Group("/api"), app)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents/cursor/models", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func storedCatalog(reported bool, ids ...string) drepo.AgentCatalog {
	cat := drepo.AgentCatalog{ModelsReported: reported, ProbedAt: time.Now()}
	for _, id := range ids {
		cat.Models = append(cat.Models, dservice.AgentModel{ID: id, Name: id, Current: true})
	}
	return cat
}
