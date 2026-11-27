package telegram

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repochannel "github.com/jahrulnr/nusashell/infrastructure/repository/channel"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// sendTestChannel is a connected Telegram channel behind a scripted Bot API —
// the same wiring the channel list uses, so SendJSON meets the real client.
func sendTestChannel(t *testing.T) (*Channel, *botAPI) {
	t.Helper()
	api := &botAPI{}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	secrets, _ := reposecret.New(":memory:")
	t.Cleanup(func() { _ = secrets.Close() })
	ch := New(repochannel.New(db), secrets, nil)
	ch.base = srv.URL
	if err := ch.Start(t.Context(), &inbox{}); err != nil {
		t.Fatal(err)
	}
	doc := `{"token":"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ","allowedUserIds":["42"]}`
	if err := ch.UpdateSettings(t.Context(), json.RawMessage(doc)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return ch.Status().State == "connected" })
	return ch, api
}

// A private chat is started from its id: the Bot API's sendMessage to that
// chat, no forum topic (threadID 0), the same rendered markup a reply sends.
func TestSendJSONStartsAPrivateChat(t *testing.T) {
	ch, api := sendTestChannel(t)
	out, err := ch.SendJSON(t.Context(), json.RawMessage(`{"target":"42","text":"**Done** with `+"`a < b`"+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		MessageID  string   `json:"messageId"`
		MessageIDs []string `json:"messageIds"`
	}
	if err := json.Unmarshal(out, &res); err != nil || res.MessageID != "1" || len(res.MessageIDs) != 1 {
		t.Fatalf("answer = %s (%v)", out, err)
	}
	api.mu.Lock()
	sent := api.sent[len(api.sent)-1]
	api.mu.Unlock()
	if sent["chat_id"] != float64(42) || sent["text"] != "<b>Done</b> with <code>a &lt; b</code>" || sent["parse_mode"] != "HTML" {
		t.Fatalf("sent = %v", sent)
	}
	if _, topic := sent["message_thread_id"]; topic {
		t.Fatalf("a private chat carried a topic: %v", sent)
	}
}

// A forum topic keeps its thread, or the message lands in the wrong stream.
func TestSendJSONKeepsAForumTopic(t *testing.T) {
	ch, api := sendTestChannel(t)
	if _, err := ch.SendJSON(t.Context(), json.RawMessage(`{"target":"-100","thread":"7","text":"hi"}`)); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	sent := api.sent[len(api.sent)-1]
	api.mu.Unlock()
	if sent["chat_id"] != float64(-100) || sent["message_thread_id"] != float64(7) {
		t.Fatalf("topic send = %v", sent)
	}
}

// A chat that never started the bot is a 403 with an instruction, not a
// mystery: nothing was sent and the owner is told what to do.
func TestSendJSONForwardsA403AsAnInstruction(t *testing.T) {
	ch, api := sendTestChannel(t)
	api.mu.Lock()
	api.refuse = &apiError{code: 403, desc: "Forbidden: bot can't initiate conversation with a user"}
	api.mu.Unlock()
	_, err := ch.SendJSON(t.Context(), json.RawMessage(`{"target":"77","text":"hi"}`))
	if err == nil {
		t.Fatal("a first message to a chat that never started the bot must fail")
	}
	ae := apperror.As(err)
	if ae.StatusCode() != 403 {
		t.Fatalf("status = %d (%v), want 403", ae.StatusCode(), err)
	}
	if !strings.Contains(ae.Error(), "started the bot") || !strings.Contains(ae.Error(), "can't initiate conversation") {
		t.Fatalf("message = %q, want the instruction and Telegram's own words", ae.Error())
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 {
		t.Fatalf("a refused message was recorded as sent: %v", api.sent)
	}
}

// A malformed request is the caller's, not Telegram's: 400 before any call.
func TestSendJSONRejectsWhatItCannotSend(t *testing.T) {
	ch, api := sendTestChannel(t)
	cases := map[string]string{
		"unknown type":  `{"target":"42","type":"sticker","text":"hi"}`,
		"blank text":    `{"target":"42","text":"   "}`,
		"no target":     `{"target":"","text":"hi"}`,
		"word target":   `{"target":"@someone","text":"hi"}`,
		"word thread":   `{"target":"-100","thread":"topic","text":"hi"}`,
		"unknown field": `{"target":"42","text":"hi","bogus":1}`,
		"not an object": `[{"target":"42","text":"hi"}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ch.SendJSON(t.Context(), json.RawMessage(body))
			if err == nil {
				t.Fatalf("%s was accepted", body)
			}
			if code := apperror.As(err).StatusCode(); code != 400 {
				t.Fatalf("%s = %d (%v), want 400", name, code, err)
			}
		})
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 {
		t.Fatalf("a rejected request still called sendMessage: %v", api.sent)
	}
}
