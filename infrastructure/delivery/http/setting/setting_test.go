package setting

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
	if rec := patch(`{"values":{"models.utility.provider":"or","models.utility.model":"m","server.bind":"192.168.18.81"}}`); rec.Code != 200 ||
		w.got["models.utility.provider"] != `"or"` || w.got["models.utility.model"] != `"m"` || w.got["server.bind"] != `"192.168.18.81"` {
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
