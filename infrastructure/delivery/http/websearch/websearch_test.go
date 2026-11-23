package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/application/setting"
	appwebsearch "github.com/jahrulnr/nusashell/application/websearch"
	"github.com/jahrulnr/nusashell/infrastructure/config"
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
func (v vault) Delete(_ context.Context, k string) error { delete(v, k); return nil }
func (v vault) Close() error                             { return nil }

func router(t *testing.T) *gin.Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(config.NewConfig())
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	app := appwebsearch.New(appwebsearch.Deps{
		Config: func() (config.WebSearchConfig, error) {
			c := config.NewConfig()
			err := c.Load(repoconfig.NewLocal(), path)
			return c.WebSearch, err
		},
		Settings: setting.New(repoconfig.NewLocal(), path),
		Secrets:  vault{},
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), app)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSettingsRoundTrip(t *testing.T) {
	r := router(t)
	if w := do(r, http.MethodGet, "/api/websearch", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"startpage"`) {
		t.Fatalf("get %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodPatch, "/api/websearch", `{"limit":20,"github.search_issues":"off"}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"limit":20`) || !strings.Contains(w.Body.String(), `"searchIssues":false`) {
		t.Fatalf("patch %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodPatch, "/api/websearch", `{"brave.key_ref":"env:X"}`); w.Code != 400 {
		t.Fatalf("patch key ref %d %s", w.Code, w.Body)
	}
	w := do(r, http.MethodPut, "/api/websearch/keys/tavily", `{"value":"tvly-abc"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "tvly-abc") || !strings.Contains(w.Body.String(), `"vaulted"`) {
		t.Fatalf("set key %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodDelete, "/api/websearch/keys/tavily", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"vaulted"`) {
		t.Fatalf("clear key %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodPost, "/api/websearch/test", `{"tool":"search","query":"x"}`); w.Code != 409 {
		t.Fatalf("test without tools %d %s", w.Code, w.Body)
	}
}
