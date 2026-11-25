package setting

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	appsetting "github.com/jahrulnr/nusashell/application/setting"
	repoconfig "github.com/jahrulnr/nusashell/infrastructure/repository/config"
)

// fakes satisfy the consumer-defined interfaces, not the app.
type fakeReader struct{ raw []byte }

func (f *fakeReader) Show() ([]byte, error) { return f.raw, nil }

type fakeWriter struct{ got map[string]string }

func (f *fakeWriter) SetMany(values map[string]string) ([]byte, error) {
	f.got = values
	return []byte(`{}`), nil
}

func router(d Deps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), d)
	return r
}

func do(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestNilReadsLeavesReadRouteUnregistered(t *testing.T) {
	r := router(Deps{Writes: &fakeWriter{}})
	if rec := do(r, "GET", "/api/settings"); rec.Code != 404 {
		t.Fatalf("GET /api/settings: want 404, got %d", rec.Code)
	}
}

func TestNilWritesLeavesWriteRouteUnregistered(t *testing.T) {
	// The path exists for GET; PATCH still stays unregistered.
	r := router(Deps{Reads: &fakeReader{raw: []byte(`{}`)}})
	if rec := do(r, "PATCH", "/api/settings"); rec.Code != 404 {
		t.Fatalf("PATCH /api/settings: want 404, got %d", rec.Code)
	}
}

func TestGetSettings(t *testing.T) {
	raw := []byte(`{"conversation":{"mailbox_cap":64,"secret_thing":"x"},"agents":{"idle_timeout":"20m0s"},"notifications":{"desktop":"off"},"storage":{"dir":"/root/x"},"server":{"bind":"127.0.0.1"},
		"models":{"utility":{"agent":"","provider":"or","model":"m"},"chat":{"agent":"claude"}}}`)
	r := router(Deps{Reads: &fakeReader{raw: raw}})
	rec := do(r, "GET", "/api/settings")
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	// Roles keep their nesting; only whitelisted leaves cross.
	if got := string(doc["models"]["utility"]); got != `{"model":"m","provider":"or"}` {
		t.Fatalf("models.utility = %s", got)
	}
	if got := string(doc["models"]["chat"]); got != `{"agent":"claude"}` {
		t.Fatalf("models.chat = %s", got)
	}
	if string(doc["conversation"]["mailbox_cap"]) != "64" {
		t.Fatalf("whitelisted leaf missing: %s", rec.Body.String())
	}
	if string(doc["agents"]["idle_timeout"]) != `"20m0s"` {
		t.Fatalf("agents.idle_timeout missing: %s", rec.Body.String())
	}
	if string(doc["notifications"]["desktop"]) != `"off"` {
		t.Fatalf("notifications.desktop missing: %s", rec.Body.String())
	}
	if string(doc["server"]["bind"]) != `"127.0.0.1"` {
		t.Fatalf("server.bind missing: %s", rec.Body.String())
	}
	if _, ok := doc["storage"]; ok {
		t.Fatalf("unlisted section crossed: %s", rec.Body.String())
	}
}

// A PATCH sets every named leaf in one write and refuses any key outside
// the whitelist without writing.
func TestPatchSettings(t *testing.T) {
	w := &fakeWriter{}
	r := router(Deps{Writes: w})
	patch := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PATCH", "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := patch(`{"values":{"models.utility.provider":"or","models.utility.model":"m","models.threads.effort":"max","server.bind":"192.168.18.81"}}`); rec.Code != 200 ||
		w.got["models.utility.provider"] != `"or"` || w.got["models.utility.model"] != `"m"` || w.got["models.threads.effort"] != `"max"` || w.got["server.bind"] != `"192.168.18.81"` {
		t.Fatalf("patch: %d %v", rec.Code, w.got)
	}
	w.got = nil
	for _, body := range []string{
		`{"values":{"models.utility.provider":"or","storage.dir":"/x"}}`,
		`{"values":{"models.utility.agent":"claude"}}`,
		`{"values":{"server.port":null}}`,
		`{"values":{}}`,
	} {
		if rec := patch(body); rec.Code != 400 || w.got != nil {
			t.Fatalf("%s: want 400 and no write, got %d %v", body, rec.Code, w.got)
		}
	}
}

// The media bridge's leaves ride the same settings document the Models page
// uses: the vision role and the send switch behind read_media. The switch is a
// boolean leaf (a JSON boolean, not a string), and the role's agent/effort
// keys stay unwritable — the vision role is a provider and a model.
func TestMediaLeavesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	app := appsetting.New(repoconfig.NewLocal(), path)
	r := router(Deps{Reads: app, Writes: app})

	get := func() map[string]any {
		t.Helper()
		rec := do(r, "GET", "/api/settings")
		if rec.Code != 200 {
			t.Fatalf("GET /api/settings = %d: %s", rec.Code, rec.Body.String())
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	patch := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PATCH", "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		return rec
	}

	// Readable before anything is set: the defaults the page shows.
	doc := get()
	media, _ := doc["media"].(map[string]any)
	if media["reading_consent"] != false {
		t.Fatalf("GET media.reading_consent = %v, want the off default", media["reading_consent"])
	}
	vision, _ := doc["models"].(map[string]any)["vision"].(map[string]any)
	if vision["provider"] != "" || vision["model"] != "" {
		t.Fatalf("GET models.vision = %v, want the empty role", vision)
	}

	// Writable in one atomic PATCH, with the boolean as a JSON literal.
	rec := patch(`{"values":{"models.vision.provider":"openrouter","models.vision.model":"google/gemini-2.5-flash","media.reading_consent":true}}`)
	if rec.Code != 200 {
		t.Fatalf("PATCH = %d: %s", rec.Code, rec.Body.String())
	}
	doc = get()
	vision, _ = doc["models"].(map[string]any)["vision"].(map[string]any)
	media, _ = doc["media"].(map[string]any)
	if vision["provider"] != "openrouter" || vision["model"] != "google/gemini-2.5-flash" || media["reading_consent"] != true {
		t.Fatalf("doc after PATCH: models.vision = %v, media = %v", vision, media)
	}

	// A string where the leaf is a boolean is refused, and nothing is written.
	if rec := patch(`{"values":{"media.reading_consent":"true"}}`); rec.Code != 400 {
		t.Fatalf("string for a boolean leaf: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if doc = get(); doc["media"].(map[string]any)["reading_consent"] != true {
		t.Fatalf("a refused write changed the switch: %v", doc["media"])
	}

	// The role is a provider and a model: its agent and effort keys stay out.
	for _, body := range []string{
		`{"values":{"models.vision.agent":"claude"}}`,
		`{"values":{"models.vision.effort":"high"}}`,
	} {
		if rec := patch(body); rec.Code != 400 {
			t.Fatalf("%s: want 400, got %d", body, rec.Code)
		}
	}
}

// A wrong-typed leaf must say which leaf and which type in the wire's detail:
// the public message travels, the cause does not.
func TestPatchNamesTheLeafAndItsType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writes := appsetting.New(repoconfig.NewLocal(), path)
	r := router(Deps{Reads: &fakeReader{raw: []byte(`{}`)}, Writes: writes})

	rec := doBody(t, r, "PATCH", "/api/settings", `{"values":{"media.reading_consent":"true"}}`)
	if rec.Code != 400 {
		t.Fatalf("wrong type = %d: %s", rec.Code, rec.Body.String())
	}
	detail := problemDetail(t, rec)
	if !strings.Contains(detail, "media.reading_consent") || !strings.Contains(detail, "boolean") {
		t.Fatalf("detail = %q, want the leaf and its expected type", detail)
	}

	// A refusal that never had a cause keeps its wording.
	rec = doBody(t, r, "PATCH", "/api/settings", `{"values":{"conversation.not_a_key":1}}`)
	if rec.Code != 400 {
		t.Fatalf("unknown leaf = %d", rec.Code)
	}
	if detail := problemDetail(t, rec); detail != "conversation.not_a_key is not a writable settings leaf" {
		t.Fatalf("unknown leaf detail = %q", detail)
	}
}

// doBody sends a JSON body with the content type the handler requires.
func doBody(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// problemDetail reads the problem+json detail of a refusal.
func problemDetail(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return body.Detail
}
