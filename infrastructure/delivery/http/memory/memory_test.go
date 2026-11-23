package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	appmemory "github.com/jahrulnr/nusashell/application/memory"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repomemory "github.com/jahrulnr/nusashell/infrastructure/repository/memory"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

func router(t *testing.T) *gin.Engine {
	r, _ := routerWithDB(t)
	return r
}

func routerWithDB(t *testing.T) (*gin.Engine, *sql.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "n.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), appmemory.New(appmemory.Deps{Memories: repomemory.New(db), Proposals: repomemory.NewProposals(db), Redact: func(s string) string { return s }}))
	return r, db
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOwnerLifecycle(t *testing.T) {
	r := router(t)
	w := do(r, http.MethodPost, "/api/memories", `{"scope":"global","type":"user","name":"editor","description":"Owner uses Helix","body":"Since 2026."}`)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var v appmemory.View
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.Source != "owner" {
		t.Fatalf("source %q", v.Source)
	}
	if w := do(r, http.MethodPost, "/api/memories", `{"scope":"global","type":"user","name":"editor","description":"x","body":"y"}`); w.Code != 409 {
		t.Fatalf("duplicate %d", w.Code)
	}
	if w := do(r, http.MethodPut, "/api/memories/"+v.ID, `{"type":"user","description":"Owner uses Zed","body":"Switched."}`); w.Code != 200 || !strings.Contains(w.Body.String(), "Zed") {
		t.Fatalf("update %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodPost, "/api/memories/"+v.ID+"/actions", `{"action":"archive"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"archived"`) {
		t.Fatalf("archive %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodGet, "/api/memories", ""); !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("default list shows forgotten: %s", w.Body)
	}
	if w := do(r, http.MethodGet, "/api/memories?status=archived&q=zed", ""); !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("archived search: %s", w.Body)
	}
	if w := do(r, http.MethodPost, "/api/memories/"+v.ID+"/actions", `{"action":"explode"}`); w.Code != 400 {
		t.Fatalf("bad action %d", w.Code)
	}
	if w := do(r, http.MethodDelete, "/api/memories/"+v.ID, ""); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := do(r, http.MethodDelete, "/api/memories/"+v.ID, ""); w.Code != 404 {
		t.Fatalf("delete again %d", w.Code)
	}
}

func TestSuggestionReview(t *testing.T) {
	r, db := routerWithDB(t)
	props := repomemory.NewProposals(db)
	add := func(name string) string {
		p, err := aggregate.NewMemoryProposal("", aggregate.MemoryProposalInput{
			ConversationID: "c_1", MessageID: "m_1", Op: enum.MemoryProposalAdd, Scope: valueobject.GlobalMemoryScope(),
			Type: enum.MemoryTypeUser, Name: name, Description: "Owner deploys on Fridays", Body: "Fridays.", Evidence: "I deploy on Fridays",
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := props.Add(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		return p.ID()
	}
	first, second := add("deploy-day"), add("other-fact")
	if w := do(r, http.MethodGet, "/api/memory-suggestions", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"pending":2`) {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	w := do(r, http.MethodPost, "/api/memory-suggestions/"+first+"/actions", `{"action":"accept","edit":{"body":"Fridays only."}}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accepted"`) {
		t.Fatalf("accept %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodGet, "/api/memories", ""); !strings.Contains(w.Body.String(), "Fridays only.") || !strings.Contains(w.Body.String(), `"consolidation"`) {
		t.Fatalf("accepted memory: %s", w.Body)
	}
	if w := do(r, http.MethodPost, "/api/memory-suggestions/"+first+"/actions", `{"action":"dismiss"}`); w.Code != 409 {
		t.Fatalf("decided twice %d", w.Code)
	}
	if w := do(r, http.MethodPost, "/api/memory-suggestions/"+second+"/actions", `{"action":"dismiss"}`); w.Code != 200 {
		t.Fatalf("dismiss %d %s", w.Code, w.Body)
	}
	if w := do(r, http.MethodGet, "/api/memory-suggestions?status=decided", ""); !strings.Contains(w.Body.String(), `"total":2`) || !strings.Contains(w.Body.String(), `"pending":0`) {
		t.Fatalf("decided %s", w.Body)
	}
	if w := do(r, http.MethodGet, "/api/memory-suggestions?status=maybe", ""); w.Code != 400 {
		t.Fatalf("bad status %d", w.Code)
	}
}
