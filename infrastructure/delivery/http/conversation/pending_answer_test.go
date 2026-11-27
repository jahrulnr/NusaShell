package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// liveRoom is a room the write routes act on. A pick taken while a turn runs
// parks; a pick taken while the room is idle applies at once and supersedes the
// parked pick of its own key, as the application does. Reads answer the state as
// it is at the call, so an answer read after a write shows what the write left.
type liveRoom struct {
	info    drepo.ConversationInfo
	busy    bool
	pending *drepo.PendingSelection
	// consumed makes a busy pick come back as parked while a turn takes it in the
	// same moment: the answer must then say the pick is applied, not waiting.
	consumed bool
	// pendingErr makes the pending read of this room fail, as a store that cannot
	// answer. extra are further rooms, which wait for nothing and read clean.
	pendingErr error
	extra      []drepo.ConversationInfo
	// annotateErr makes the room refuse a write, as the application does for a
	// pick the room cannot take.
	annotateErr error
}

func (r *liveRoom) park(fill func(p *drepo.PendingSelection)) {
	if r.consumed {
		return
	}
	if r.pending == nil {
		r.pending = &drepo.PendingSelection{Options: map[string]string{}}
	}
	fill(r.pending)
}

func (r *liveRoom) settle() {
	if r.pending != nil && r.pending.Empty() {
		r.pending = nil
	}
}

func (r *liveRoom) Info(context.Context, string) (drepo.ConversationInfo, error) {
	return r.info, nil
}

func (r *liveRoom) CanCompact(context.Context, string) (bool, error) { return false, nil }

func (r *liveRoom) List(context.Context) ([]drepo.ConversationInfo, error) {
	return append([]drepo.ConversationInfo{r.info}, r.extra...), nil
}

func (r *liveRoom) History(context.Context, string, int, int) ([]entity.Message, error) {
	return nil, nil
}

func (r *liveRoom) ContextFooterStats(context.Context, string, string, string) (appconversation.ContextFooterStats, error) {
	return appconversation.ContextFooterStats{}, nil
}

func (r *liveRoom) Pending(_ context.Context, convID string) (drepo.PendingSelection, bool, error) {
	if convID != r.info.ID {
		return drepo.PendingSelection{}, false, nil
	}
	if r.pendingErr != nil {
		return drepo.PendingSelection{}, false, r.pendingErr
	}
	if r.pending == nil {
		return drepo.PendingSelection{}, false, nil
	}
	return *r.pending, true, nil
}

func (r *liveRoom) AgentOptions(context.Context, string) ([]dservice.AgentOption, dservice.OptionsState, dservice.OptionsSource, error) {
	return []dservice.AgentOption{{ID: "mode", Name: "Mode", Type: "select", Current: "ask"}}, dservice.OptionsReady, dservice.OptionsSourceSession, nil
}

func (r *liveRoom) SetAgentOption(_ context.Context, _ string, id, value string) (bool, error) {
	if r.busy {
		r.park(func(p *drepo.PendingSelection) { p.Options[id] = value })
		return true, nil
	}
	if r.pending != nil {
		delete(r.pending.Options, id)
		r.settle()
	}
	return false, nil
}

func (r *liveRoom) ApprovalRules(context.Context, string) ([]aggregate.ApprovalRule, error) {
	return nil, nil
}

func (r *liveRoom) DeleteApprovalRule(context.Context, string, string) error { return nil }

func (r *liveRoom) Annotate(_ context.Context, _ string, a appconversation.Annotation) (bool, error) {
	if r.annotateErr != nil {
		return false, r.annotateErr
	}
	if a.Runtime == nil {
		return false, nil
	}
	if r.busy {
		r.park(func(p *drepo.PendingSelection) { p.AgentID, p.AgentModel = a.Runtime.AgentID, a.Runtime.AgentModel })
		return true, nil
	}
	if r.pending != nil {
		r.pending.AgentID, r.pending.AgentModel = "", ""
		r.settle()
	}
	return false, nil
}

func (r *liveRoom) Delete(context.Context, string) error { return nil }

func (r *liveRoom) DeleteAll(context.Context) (int, error) { return 0, nil }

// serveRoom sends one request through Register, with the room as the reads, the
// writes, and the agent surface, and answers the recorder.
func serveRoom(t *testing.T, room *liveRoom, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), Deps{Reads: room, Writes: room, Agent: room})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

// bodyFields decodes an answer's top-level keys. A key present with a null value
// stays in the map: that distinction is what the wire promises.
func bodyFields(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("answer is not an object (%d): %v — %s", rec.Code, err, rec.Body.String())
	}
	return fields
}

const putMode = `{"id":"mode","value":"ask"}`

// An idle pick supersedes the parked pick of its own key, and the answer shows
// what the write left: the other key still waits and the superseded one is gone.
// An answer read before the write would still show both keys.
func TestIdlePickAnswersTheStateTheWriteLeft(t *testing.T) {
	room := &liveRoom{
		info:    drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		pending: &drepo.PendingSelection{Options: map[string]string{"mode": "plan", "strict": "true"}},
	}
	rec := serveRoom(t, room, http.MethodPut, "/api/conversations/c_room/agent-options", putMode)
	if rec.Code != http.StatusOK {
		t.Fatalf("idle pick = %d, want 200 — it applied at once: %s", rec.Code, rec.Body.String())
	}
	if fields := bodyFields(t, rec); string(fields["source"]) != `"session"` {
		t.Fatalf("agent-options answer source = %s, want session", fields["source"])
	}
	var answer struct {
		Pending *pendingDTO `json:"pending"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	want := []pendingOption{{ID: "strict", Value: "true"}}
	if answer.Pending == nil || len(answer.Pending.Options) != len(want) || answer.Pending.Options[0] != want[0] {
		t.Fatalf("answer pending = %+v, want only strict=true — the superseded mode pick must not come back in the answer", answer.Pending)
	}
}

// An idle pick that supersedes the last parked pick answers "pending": null. The
// key is present, so a client reads the same shape it reads from a room with
// nothing waiting, and absent never stands for empty.
func TestIdlePickAnswersNullWhenNothingWaits(t *testing.T) {
	room := &liveRoom{
		info:    drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		pending: &drepo.PendingSelection{Options: map[string]string{"mode": "plan"}},
	}
	rec := serveRoom(t, room, http.MethodPut, "/api/conversations/c_room/agent-options", putMode)
	raw, ok := bodyFields(t, rec)["pending"]
	if !ok {
		t.Fatal("answer carries no pending key — absent and null must not stand for different things")
	}
	if string(raw) != "null" {
		t.Fatalf("pending = %s, want null: nothing waits", raw)
	}
}

// A pick taken while a turn runs answers 202 with the waiting pick, and that
// object is the same bytes the room projection reads afterwards.
func TestBusyPickAnswersTheWaitingPickInTheProjectionsShape(t *testing.T) {
	room := &liveRoom{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}, busy: true}
	rec := serveRoom(t, room, http.MethodPut, "/api/conversations/c_room/agent-options", `{"id":"mode","value":"build"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("busy pick = %d, want 202 — it waits for the next turn: %s", rec.Code, rec.Body.String())
	}
	answered := string(bodyFields(t, rec)["pending"])
	if !strings.Contains(answered, `"value":"build"`) {
		t.Fatalf("answer pending = %s, want the waiting build pick", answered)
	}
	detail := serveRoom(t, room, http.MethodGet, "/api/conversations/c_room", "")
	if read := string(bodyFields(t, detail)["pending"]); read != answered {
		t.Fatalf("answer pending %s, room projection %s — one object, one shape", answered, read)
	}
}

// A turn can take the pick in the moment between the write and the answer. The
// answer then says what is true: the pick is applied, so it is a plain 200 with
// null, never a 202 that promises a waiting pick nobody holds.
func TestAPickTakenByTheTurnAnswersPlain200(t *testing.T) {
	room := &liveRoom{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}, busy: true, consumed: true}
	rec := serveRoom(t, room, http.MethodPut, "/api/conversations/c_room/agent-options", putMode)
	if rec.Code != http.StatusOK {
		t.Fatalf("option answer = %d, want 200: the turn applied the pick", rec.Code)
	}
	if raw := string(bodyFields(t, rec)["pending"]); raw != "null" {
		t.Fatalf("pending = %s, want null: nothing waits", raw)
	}
	patch := serveRoom(t, room, http.MethodPatch, "/api/conversations/c_room", `{"agentId":"fake","modelId":"swe-2-1"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("runtime answer = %d, want 200: the turn applied the pick", patch.Code)
	}
}

// If the pending read fails after the write, the answer still comes: the option
// landed, and only the read is unknown. The answer says so with pendingUnreadable
// rather than a bare null, which would read as "nothing waits".
func TestPendingReadFailureAfterTheWriteAnswersUnreadable(t *testing.T) {
	room := &liveRoom{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}, pendingErr: errors.New("store unreadable")}
	rec := serveRoom(t, room, http.MethodPut, "/api/conversations/c_room/agent-options", putMode)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d, want 200: the option landed and only the read is unknown: %s", rec.Code, rec.Body.String())
	}
	fields := bodyFields(t, rec)
	if raw := string(fields["pending"]); raw != "null" {
		t.Fatalf("pending = %s, want null", raw)
	}
	if raw := string(fields["pendingUnreadable"]); raw != "true" {
		t.Fatalf("pendingUnreadable = %s, want true", raw)
	}
}

// A runtime pick through PATCH follows the same rule: idle, it supersedes the
// parked runtime and answers 200 with null; busy, it answers 202 with the waiting
// runtime pick.
func TestPatchAnswersThePendingStateAfterTheWrite(t *testing.T) {
	const body = `{"agentId":"fake","modelId":"swe-2-1"}`
	idle := &liveRoom{
		info:    drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		pending: &drepo.PendingSelection{AgentID: "fake", AgentModel: "stale-model"},
	}
	rec := serveRoom(t, idle, http.MethodPatch, "/api/conversations/c_room", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("idle runtime pick = %d, want 200", rec.Code)
	}
	if raw := string(bodyFields(t, rec)["pending"]); raw != "null" {
		t.Fatalf("idle PATCH answers pending %s, want null: the parked runtime was superseded", raw)
	}

	busy := &liveRoom{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}, busy: true}
	rec = serveRoom(t, busy, http.MethodPatch, "/api/conversations/c_room", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("busy runtime pick = %d, want 202", rec.Code)
	}
	if raw := string(bodyFields(t, rec)["pending"]); !strings.Contains(raw, `"modelId":"swe-2-1"`) {
		t.Fatalf("busy PATCH answers pending %s, want the waiting swe-2-1 pick", raw)
	}
}

// Pending is part of every room projection, not an extra on some: detail, list,
// and export carry it as null when nothing waits, the same key a waiting pick
// fills.
func TestEveryRoomProjectionCarriesPendingAsNullWhenNothingWaits(t *testing.T) {
	room := &liveRoom{info: drepo.ConversationInfo{ID: "c_room", AgentID: "fake"}}
	if raw := string(bodyFields(t, serveRoom(t, room, http.MethodGet, "/api/conversations/c_room", ""))["pending"]); raw != "null" {
		t.Fatalf("detail pending = %s, want null", raw)
	}
	var list struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	rec := serveRoom(t, room, http.MethodGet, "/api/conversations", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Data) != 1 {
		t.Fatalf("list = %d rows, %v: %s", len(list.Data), err, rec.Body.String())
	}
	if raw, ok := list.Data[0]["pending"]; !ok || string(raw) != "null" {
		t.Fatalf("list pending = %s (present %v), want null", raw, ok)
	}
	var export struct {
		Conversation map[string]json.RawMessage `json:"conversation"`
	}
	rec = serveRoom(t, room, http.MethodGet, "/api/conversations/c_room/export", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &export); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	if raw, ok := export.Conversation["pending"]; !ok || string(raw) != "null" {
		t.Fatalf("export pending = %s (present %v), want null — the export read it, nothing waits", raw, ok)
	}
}

// A PATCH that carries an effort an agent room cannot take is refused with the
// application's 400 and its sentence. It never answers 200 with the room read back
// as if the pick had landed.
func TestPatchAnEffortOnAnAgentRoomAnswers400(t *testing.T) {
	room := &liveRoom{
		info:        drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		annotateErr: apperror.New(400, "this room runs an agent, which takes no effort pick: effort and upstream are settings of a provider room", nil),
	}
	rec := serveRoom(t, room, http.MethodPatch, "/api/conversations/c_room", `{"effort":"max"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "takes no effort pick") {
		t.Fatalf("the refusal does not say why: %s", rec.Body.String())
	}
}
