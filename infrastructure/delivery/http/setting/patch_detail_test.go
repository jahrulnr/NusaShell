package setting

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appsetting "github.com/jahrulnr/nusashell/application/setting"
	repoconfig "github.com/jahrulnr/nusashell/infrastructure/repository/config"
)

// A refused value must say which leaf it was and what that leaf accepts: the
// Models page renders the problem's detail verbatim, and "invalid value"
// alone tells the owner neither.
func TestPatchTypeMismatchNamesLeafAndExpectedType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	app := appsetting.New(repoconfig.NewLocal(), path)
	r := router(Deps{Reads: app, Writes: app})

	patch := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PATCH", "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		return rec
	}

	for _, c := range []struct{ body, leaf, accepts string }{
		{`{"values":{"media.reading_consent":"true"}}`, "media.reading_consent", "boolean value like false"},
		{`{"values":{"conversation.max_turn_steps":"abc"}}`, "conversation.max_turn_steps", "number value like 512"},
	} {
		rec := patch(c.body)
		if rec.Code != 400 {
			t.Fatalf("%s: want 400, got %d: %s", c.body, rec.Code, rec.Body.String())
		}
		var problem struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(problem.Detail, c.leaf) || !strings.Contains(problem.Detail, c.accepts) {
			t.Fatalf("%s: detail = %q, want it to name %q and %q", c.body, problem.Detail, c.leaf, c.accepts)
		}
	}

	// Neither refusal wrote anything: the document still holds its defaults.
	rec := do(r, "GET", "/api/settings")
	var doc struct {
		Media struct {
			ReadingConsent any `json:"reading_consent"`
		} `json:"media"`
		Conversation struct {
			MaxTurnSteps any `json:"max_turn_steps"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Media.ReadingConsent != false {
		t.Fatalf("a refused PATCH changed media.reading_consent: %v", doc.Media.ReadingConsent)
	}
	if doc.Conversation.MaxTurnSteps != float64(512) {
		t.Fatalf("a refused PATCH changed conversation.max_turn_steps: %v", doc.Conversation.MaxTurnSteps)
	}
}
