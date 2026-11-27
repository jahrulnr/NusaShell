package conversation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// List and detail both write lifecycle. An interrupted terminal and a
// terminal that cannot be read are never idle.
func TestListAndDetailDoNotMapInterruptedOrUnreadableToIdle(t *testing.T) {
	room := &liveRoom{
		info:    drepo.ConversationInfo{ID: "c_cut", Terminal: "interrupted"},
		histErr: errors.New("history unreadable"),
		extra: []drepo.ConversationInfo{
			{ID: "c_dark", MessageCount: 3},
			{ID: "c_fail", Terminal: "failed"},
		},
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), Deps{Reads: room, Writes: room, Agent: room})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	rows := listRowsByID(t, rec)
	for _, id := range []string{"c_cut", "c_dark", "c_fail"} {
		raw, ok := rows[id]["lifecycle"]
		if !ok {
			t.Fatalf("%s omitted lifecycle: %s", id, rec.Body.String())
		}
		var word string
		if err := json.Unmarshal(raw, &word); err != nil {
			t.Fatal(err)
		}
		if word == "idle" {
			t.Fatalf("%s lifecycle = idle", id)
		}
		rows[id]["lifecycle"] = raw
		switch id {
		case "c_cut":
			if word != "interrupted" {
				t.Fatalf("interrupted = %q", word)
			}
		case "c_dark":
			if word != "unknown" {
				t.Fatalf("unreadable = %q", word)
			}
		case "c_fail":
			if word != "failed" {
				t.Fatalf("failed = %q", word)
			}
		}
	}
	detail := httptest.NewRecorder()
	r.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/conversations/c_cut", nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"lifecycle":"interrupted"`) {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
}

// meta.terminal completed while the tail yields no messages is not idle,
// and a compact read that fails does not turn detail into a 500.
func TestUnreadableTailIsUnknownOnListAndDetail(t *testing.T) {
	room := &liveRoom{
		info: drepo.ConversationInfo{
			ID: "c_gone", Terminal: "completed", MessageCount: 5, ActiveCount: 5,
		},
		compactErr: errors.New("active.jsonl empty while metadata records 5 tail messages"),
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), Deps{Reads: room, Writes: room})
	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/conversations", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	rows := listRowsByID(t, list)
	var word string
	if err := json.Unmarshal(rows["c_gone"]["lifecycle"], &word); err != nil {
		t.Fatalf("list lifecycle: %s", list.Body.String())
	}
	if word != "unknown" {
		t.Fatalf("unreadable history = %q, want unknown", word)
	}
	detail := httptest.NewRecorder()
	r.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/conversations/c_gone", nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"lifecycle":"unknown"`) {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
}
