package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// roomReads serves one room to the detail route. The projection reads only
// Info, CanCompact and Pending; the rest of the surface is never reached.
type roomReads struct {
	info    drepo.ConversationInfo
	pending *drepo.PendingSelection
}

func (r roomReads) Info(context.Context, string) (drepo.ConversationInfo, error) {
	return r.info, nil
}

func (r roomReads) CanCompact(context.Context, string) (bool, error) { return false, nil }

func (r roomReads) List(context.Context) ([]drepo.ConversationInfo, error) { return nil, nil }

func (r roomReads) History(context.Context, string, int, int) ([]entity.Message, error) {
	return nil, nil
}

func (r roomReads) ContextFooterStats(context.Context, string, string, string) (appconversation.ContextFooterStats, error) {
	return appconversation.ContextFooterStats{}, nil
}

func (r roomReads) Pending(context.Context, string) (drepo.PendingSelection, bool, error) {
	if r.pending == nil {
		return drepo.PendingSelection{}, false, nil
	}
	return *r.pending, true, nil
}

// roomAgent answers the room's option reading and counts the asks. empty makes
// it a live session that has offered nothing yet.
type roomAgent struct {
	opts        []dservice.AgentOption
	err         error
	empty       bool
	unavailable bool // the state, answered without an error
	source      dservice.OptionsSource
	calls       int
}

func (a *roomAgent) AgentOptions(context.Context, string) ([]dservice.AgentOption, dservice.OptionsState, dservice.OptionsSource, error) {
	a.calls++
	if a.err != nil {
		return nil, dservice.OptionsUnavailable, dservice.OptionsSourceNone, a.err
	}
	if a.unavailable {
		return nil, dservice.OptionsUnavailable, dservice.OptionsSourceNone, nil
	}
	if a.empty {
		return nil, dservice.OptionsEmpty, dservice.OptionsSourceSession, nil
	}
	source := a.source
	if source == dservice.OptionsSourceNone {
		source = dservice.OptionsSourceSession
	}
	return a.opts, dservice.OptionsReady, source, nil
}

func (a *roomAgent) SetAgentOption(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (a *roomAgent) ApprovalRules(context.Context, string) ([]aggregate.ApprovalRule, error) {
	return nil, nil
}

func (a *roomAgent) DeleteApprovalRule(context.Context, string, string) error { return nil }

// getRoom serves GET /api/conversations/c_room through Register and answers
// the body's top-level keys, so a test can tell an absent field from a
// present one.
func getRoom(t *testing.T, reads Reader, agent AgentSurface) map[string]json.RawMessage {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), Deps{Reads: reads, Agent: agent})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/conversations/c_room", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET room = %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("room body is not an object: %v", err)
	}
	return body
}

// agentRoomOptions decodes the room's agentOptions object, failing when the key
// is absent: the caller asserts presence on purpose.
func agentRoomOptions(t *testing.T, body map[string]json.RawMessage) agentOptionsDTO {
	t.Helper()
	raw, ok := body["agentOptions"]
	if !ok {
		t.Fatal("agent room carries no agentOptions")
	}
	var got agentOptionsDTO
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("agentOptions = %s: %v", raw, err)
	}
	return got
}

// An agent room reports the values its session runs, and no effort: effort is
// the provider's reasoning effort, empty on a room that runs an agent.
func TestAgentRoomReportsItsOptionsAndNoEffort(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	agent := &roomAgent{opts: []dservice.AgentOption{
		{ID: "mode", Name: "Mode", Type: "select", Current: "build"},
		{ID: "strict", Name: "Strict", Type: "boolean", Current: "false"},
	}}
	body := getRoom(t, reads, agent)
	if raw, ok := body["effort"]; ok {
		t.Fatalf("agent room carries effort %s — effort stays the provider's and is empty here", raw)
	}
	want := []agentOptionDTO{{ID: "mode", Name: "Mode", Value: "build"}, {ID: "strict", Name: "Strict", Value: "false"}}
	got := agentRoomOptions(t, body)
	if got.Status != "ready" || !slices.Equal(got.Data, want) {
		t.Fatalf("agentOptions = %+v, want ready with %+v", got, want)
	}
	if _, ok := body["agentOptionsUnreadable"]; ok {
		t.Fatal("a readable agent room carries agentOptionsUnreadable")
	}
}

// A list that did not come from the live session is marked as fallback evidence
// on the wire: a picker may show it, but never as the options in force now.
func TestAgentOptionsWireMarksCatalogSource(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	agent := &roomAgent{
		source: dservice.OptionsSourceCatalog,
		opts:   []dservice.AgentOption{{ID: "mode", Name: "Mode", Type: "select", Current: "build"}},
	}
	got := agentRoomOptions(t, getRoom(t, reads, agent))
	if got.Source != string(dservice.OptionsSourceCatalog) {
		t.Fatalf("agentOptions source = %q, want catalog", got.Source)
	}
}

// A provider room keeps its effort and carries neither agent field: the agent is
// not even asked, because the room has none to ask.
func TestProviderRoomReportsEffortAndNoAgentOptions(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", ProviderID: "openai", ModelID: "gpt-x", Effort: "high"}}
	agent := &roomAgent{opts: []dservice.AgentOption{{ID: "mode", Type: "select", Current: "build"}}}
	body := getRoom(t, reads, agent)
	if raw, ok := body["agentOptions"]; ok {
		t.Fatalf("provider room carries agentOptions %s", raw)
	}
	if raw, ok := body["agentOptionsUnreadable"]; ok {
		t.Fatalf("provider room carries agentOptionsUnreadable %s — it has no agent to be unreadable", raw)
	}
	if got := string(body["effort"]); got != `"high"` {
		t.Fatalf("effort = %s, want the provider's high", got)
	}
	if agent.calls != 0 {
		t.Fatalf("provider room asked its agent %d times", agent.calls)
	}
}

// Only what the session runs is reported: an option with no current value
// holds nothing, and a deferred entry is a stored pick the agent does not
// offer, which the session never applies.
func TestAgentOptionsReportOnlyTheValuesTheSessionRuns(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	agent := &roomAgent{opts: []dservice.AgentOption{
		{ID: "mode", Name: "Mode", Type: "select", Current: "build"},
		{ID: "effort", Name: "Effort", Type: "select"},
		{ID: "plan", Name: "plan", Type: "select", Deferred: "ask"},
	}}
	want := []agentOptionDTO{{ID: "mode", Name: "Mode", Value: "build"}}
	if got := agentRoomOptions(t, getRoom(t, reads, agent)); !slices.Equal(got.Data, want) {
		t.Fatalf("agentOptions = %+v, want only the value in use: %+v", got, want)
	}
}

// An agent with no options in use answers ready with an empty list, not null and
// not an absent field: the room is an agent room, and nothing is in use.
func TestAgentRoomWithNoOptionsAnswersAnEmptyList(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	body := getRoom(t, reads, &roomAgent{})
	got := agentRoomOptions(t, body)
	if got.Status != "ready" || len(got.Data) != 0 {
		t.Fatalf("agentOptions = %+v, want ready with none in use", got)
	}
	if !strings.Contains(string(body["agentOptions"]), `"data":[]`) {
		t.Fatalf("agentOptions = %s, want an empty list, not null", body["agentOptions"])
	}
}

// A live session that has offered nothing yet answers empty, not ready: that
// state is transient, so the wire must not tell the picker the list is settled.
func TestEmptyLiveSessionAnswersEmptyNotReady(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	body := getRoom(t, reads, &roomAgent{empty: true})
	got := agentRoomOptions(t, body)
	if got.Status != "empty" || len(got.Data) != 0 {
		t.Fatalf("agentOptions = %+v, want empty with none in use", got)
	}
	if _, ok := body["agentOptionsUnreadable"]; ok {
		t.Fatal("an answered empty session carries agentOptionsUnreadable")
	}
}

// When the agent cannot be asked the room still opens: agentOptions is absent and
// agentOptionsUnreadable says why. Absence alone would read as "no options", which
// is not what the agent said.
func TestUnreadableAgentOptionsLeaveTheFieldAbsent(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	agent := &roomAgent{err: errors.New("agent not running")}
	body := getRoom(t, reads, agent)
	if raw, ok := body["agentOptions"]; ok {
		t.Fatalf("unreadable agent answered agentOptions %s — it must be absent", raw)
	}
	if got := string(body["agentOptionsUnreadable"]); got != "true" {
		t.Fatalf("agentOptionsUnreadable = %q, want true", got)
	}
}

// unavailable is a reading state that never reaches the wire, even when the
// reading returns no error: the room says its options are unreadable, and no
// status names the state.
func TestUnavailableStateNeverReachesTheWire(t *testing.T) {
	reads := roomReads{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	body := getRoom(t, reads, &roomAgent{unavailable: true})
	if raw, ok := body["agentOptions"]; ok {
		t.Fatalf("an unavailable reading answered agentOptions %s — unavailable is never a wire status", raw)
	}
	if got := string(body["agentOptionsUnreadable"]); got != "true" {
		t.Fatalf("agentOptionsUnreadable = %q, want true", got)
	}
}

// A pick waiting for the next turn is not in use. The room reports the value
// the session runs, and the pick stays in pending where it waits.
func TestWaitingPickIsNotReportedAsAnOptionInUse(t *testing.T) {
	reads := roomReads{
		info:    drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		pending: &drepo.PendingSelection{AgentID: "fake", Options: map[string]string{"mode": "plan"}},
	}
	agent := &roomAgent{opts: []dservice.AgentOption{{ID: "mode", Name: "Mode", Type: "select", Current: "build"}}}
	body := getRoom(t, reads, agent)
	want := []agentOptionDTO{{ID: "mode", Name: "Mode", Value: "build"}}
	if got := agentRoomOptions(t, body); !slices.Equal(got.Data, want) {
		t.Fatalf("agentOptions = %+v, want the value in use %+v", got, want)
	}
	if raw := string(body["pending"]); !strings.Contains(raw, `"value":"plan"`) {
		t.Fatalf("pending = %s, want the waiting plan pick", raw)
	}
}
