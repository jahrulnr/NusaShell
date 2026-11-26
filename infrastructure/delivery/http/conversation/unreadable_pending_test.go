package conversation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// listRowsByID answers the list page's rows keyed by room id, each row's wire
// keys kept as the owner's client reads them.
func listRowsByID(t *testing.T, rec *httptest.ResponseRecorder) map[string]map[string]json.RawMessage {
	t.Helper()
	var page struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("list is not a page: %v — %s", err, rec.Body.String())
	}
	rows := map[string]map[string]json.RawMessage{}
	for _, row := range page.Data {
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil {
			t.Fatalf("row carries no id: %s", row["id"])
		}
		rows[id] = row
	}
	return rows
}

// One room's unreadable waiting pick must not fail the list: every room keeps its
// row, and the unreadable one says so with pending null and the flag.
func TestListKeepsEveryRowWhenOneWaitingPickIsUnreadable(t *testing.T) {
	room := &liveRoom{
		info:       drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		pendingErr: errors.New("pending selection corrupt"),
		extra:      []drepo.ConversationInfo{{ID: "c_ok", AgentID: "fake"}},
	}
	rec := serveRoom(t, room, http.MethodGet, "/api/conversations", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200: one unreadable pick must not fail the list: %s", rec.Code, rec.Body.String())
	}
	rows := listRowsByID(t, rec)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want both rooms", len(rows))
	}
	bad, good := rows["c_room"], rows["c_ok"]
	if string(bad["pending"]) != "null" || string(bad["pendingUnreadable"]) != "true" {
		t.Fatalf("unreadable row: pending %s, pendingUnreadable %s — want null and true", bad["pending"], bad["pendingUnreadable"])
	}
	if string(good["pending"]) != "null" {
		t.Fatalf("readable row pending = %s, want null", good["pending"])
	}
	if raw, ok := good["pendingUnreadable"]; ok {
		t.Fatalf("readable row carries pendingUnreadable %s", raw)
	}
}

// An export is the whole thread, and an unreadable waiting pick does not block it:
// the thread keeps its messages, with pending null and the flag that says why.
func TestExportKeepsTheThreadWhenItsWaitingPickIsUnreadable(t *testing.T) {
	room := &liveRoom{
		info:       drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		pendingErr: errors.New("pending selection corrupt"),
	}
	rec := serveRoom(t, room, http.MethodGet, "/api/conversations/c_room/export", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var export struct {
		Conversation map[string]json.RawMessage `json:"conversation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &export); err != nil {
		t.Fatalf("export is not JSON: %v", err)
	}
	if string(export.Conversation["pending"]) != "null" || string(export.Conversation["pendingUnreadable"]) != "true" {
		t.Fatalf("export: pending %s, pendingUnreadable %s — want null and true",
			export.Conversation["pending"], export.Conversation["pendingUnreadable"])
	}
}

// A room's own detail answers when its waiting pick cannot be read, the same as
// the list does: the owner opens that room, so the room opens with pending null
// and pendingUnreadable set. The flag is what keeps the null from reading as
// "nothing waits".
func TestDetailDegradesWhenItsOwnWaitingPickIsUnreadable(t *testing.T) {
	room := &liveRoom{
		info:       drepo.ConversationInfo{ID: "c_room", AgentID: "fake"},
		pendingErr: errors.New("pending selection corrupt"),
	}
	rec := serveRoom(t, room, http.MethodGet, "/api/conversations/c_room", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d, want 200: the room opens with its pick unknown: %s", rec.Code, rec.Body.String())
	}
	fields := bodyFields(t, rec)
	if string(fields["pending"]) != "null" || string(fields["pendingUnreadable"]) != "true" {
		t.Fatalf("detail: pending %s, pendingUnreadable %s — want null and true", fields["pending"], fields["pendingUnreadable"])
	}
}
