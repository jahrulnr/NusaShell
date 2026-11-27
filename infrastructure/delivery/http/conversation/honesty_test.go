package conversation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

func TestListAndDetailCarryTheHoldOnlyWhileItApplies(t *testing.T) {
	t.Cleanup(func() { appjob.ClearAgentHold("c_hold") })
	room := &liveRoom{info: drepo.ConversationInfo{
		ID: "c_hold", AgentID: "claude", Runtime: enum.RuntimeAgent,
	}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), Deps{Reads: room, Writes: room, Agent: room})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/conversations", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	row := listRowsByID(t, rec)["c_hold"]
	if _, ok := row["agentClass"]; ok {
		t.Fatalf("an unheld room wrote agentClass: %s", row["agentClass"])
	}
	var runtime string
	if err := json.Unmarshal(row["runtime"], &runtime); err != nil || runtime != "agent" {
		t.Fatalf("runtime = %s err = %v", row["runtime"], err)
	}

	at := time.Now().Add(time.Second).UnixMilli()
	appjob.NoteAgentHold("c_hold", appjob.AgentHold{
		Class: "rate_limited", Retry: "none", RetryAt: at, Message: "rate limit exceeded",
	})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/conversations", nil))
	row = listRowsByID(t, rec)["c_hold"]
	assertHold(t, row, at)

	detail := httptest.NewRecorder()
	r.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/conversations/c_hold", nil))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	assertHold(t, body, at)

	appjob.NoteAgentHold("c_hold", appjob.AgentHold{
		Class: "unknown", Retry: "none", Message: "the compiler exploded",
	})
	plain := httptest.NewRecorder()
	r.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/conversations/c_hold", nil))
	if plain.Code != http.StatusOK {
		t.Fatalf("detail = %d %s", plain.Code, plain.Body.String())
	}
	body = map[string]json.RawMessage{}
	if json.Unmarshal(plain.Body.Bytes(), &body) != nil {
		t.Fatal("detail")
	}
	if _, ok := body["retryAt"]; ok {
		t.Fatalf("a hold with no named delay wrote retryAt: %s", body["retryAt"])
	}
	var class, msg string
	_ = json.Unmarshal(body["agentClass"], &class)
	_ = json.Unmarshal(body["agentMessage"], &msg)
	if class != "unknown" || msg != "the compiler exploded" {
		t.Fatalf("class %q message %q", class, msg)
	}
}

func assertHold(t *testing.T, row map[string]json.RawMessage, at int64) {
	t.Helper()
	var class, retry, msg string
	var retryAt int64
	if json.Unmarshal(row["agentClass"], &class) != nil || class != "rate_limited" {
		t.Fatalf("agentClass = %s", row["agentClass"])
	}
	if json.Unmarshal(row["retry"], &retry) != nil || retry != "none" {
		t.Fatalf("retry = %s", row["retry"])
	}
	if json.Unmarshal(row["retryAt"], &retryAt) != nil || retryAt != at {
		t.Fatalf("retryAt = %s", row["retryAt"])
	}
	if json.Unmarshal(row["agentMessage"], &msg) != nil || msg != "rate limit exceeded" {
		t.Fatalf("agentMessage = %s", row["agentMessage"])
	}
}
