package telegram

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repochannel "github.com/jahrulnr/nusashell/infrastructure/repository/channel"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// connected starts a channel against api with one allowed user, 42, and an
// allowed group, -100, that answers mentions only.
func connected(t *testing.T, api *botAPI, in *inbox) *Channel {
	t.Helper()
	ctx := t.Context()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	secrets, _ := reposecret.New(":memory:")
	t.Cleanup(func() { secrets.Close() })
	ch := New(repochannel.New(db), secrets, nil)
	ch.base = srv.URL
	if err := ch.Start(ctx, in); err != nil {
		t.Fatal(err)
	}
	doc := `{"token":"123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ","allowedUserIds":["42"],"allowedGroupIds":["-100"],"groupPolicy":"mention"}`
	if err := ch.UpdateSettings(ctx, json.RawMessage(doc)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return ch.Status().State == "connected" })
	t.Cleanup(func() { _ = ch.Stop() })
	return ch
}

func byID(msgs []dservice.InboundMessage, id string) (dservice.InboundMessage, bool) {
	for _, m := range msgs {
		if m.MessageID == id {
			return m, true
		}
	}
	return dservice.InboundMessage{}, false
}

// Media is downloaded into the message; an album arrives as one message;
// a file over the Bot API's limit leaves a marker instead.
func TestTelegramInboundMedia(t *testing.T) {
	api := &botAPI{files: map[string][]byte{"ph1": []byte("\xff\xd8\xffjpeg"), "d1": []byte("one"), "d2": []byte("two")}}
	in := &inbox{}
	connected(t, api, in)
	private := tgChat{ID: 42, Type: "private"}
	from := &tgUser{ID: 42, FirstName: "Ana"}
	now := time.Now().Unix()
	api.mu.Lock()
	api.queue = []tgUpdate{
		{UpdateID: 1, Message: &tgMessage{MessageID: 1, Chat: private, From: from, Date: now, Caption: "what is this",
			Photo: []tgPhotoSize{{FileID: "small", FileSize: 10}, {FileID: "ph1", FileSize: 7}}}},
		{UpdateID: 2, Message: &tgMessage{MessageID: 2, Chat: private, From: from, Date: now, MediaGroupID: "g1", Caption: "both",
			Document: &tgMedia{FileID: "d1", FileName: "a.txt", MIME: "text/plain"}}},
		{UpdateID: 3, Message: &tgMessage{MessageID: 3, Chat: private, From: from, Date: now, MediaGroupID: "g1",
			Document: &tgMedia{FileID: "d2", FileName: "b.txt", MIME: "text/plain"}}},
		{UpdateID: 4, Message: &tgMessage{MessageID: 4, Chat: private, From: from, Date: now,
			Video: &tgMedia{FileID: "big", FileSize: 30 << 20}}},
		{UpdateID: 5, Message: &tgMessage{MessageID: 5, Chat: private, From: from, Date: now, Voice: &tgMedia{FileID: "gone"}}},
	}
	api.mu.Unlock()
	waitFor(t, func() bool { return len(in.messages()) == 4 })
	got := in.messages()

	photo, _ := byID(got, "42:1")
	if photo.Text != "what is this" || len(photo.Files) != 1 || photo.Files[0].Name != "photo-1.jpg" ||
		photo.Files[0].MediaType != "image/jpeg" || string(photo.Files[0].Data) != "\xff\xd8\xffjpeg" {
		t.Fatalf("photo = %+v", photo)
	}
	album, ok := byID(got, "42:2")
	if !ok || album.Text != "both" || len(album.Files) != 2 || album.Files[0].Name != "a.txt" || string(album.Files[1].Data) != "two" {
		t.Fatalf("album = %+v", album)
	}
	if _, ok := byID(got, "42:3"); ok {
		t.Fatal("an album item arrived on its own")
	}
	big, _ := byID(got, "42:4")
	if len(big.Files) != 0 || big.Text != "[Video is larger than 20 MB and was not downloaded]" {
		t.Fatalf("big = %+v", big)
	}
	gone, _ := byID(got, "42:5")
	if len(gone.Files) != 0 || gone.Text != "[Voice note could not be downloaded]" {
		t.Fatalf("failed download = %+v", gone)
	}
}

// Files go out with the Bot API method their type calls for.
func TestTelegramSendFile(t *testing.T) {
	api := &botAPI{}
	ch := connected(t, api, &inbox{})
	chat, _ := valueobject.NewChatRef("telegram", "42", "7")
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32))
	files := []dservice.ChannelFile{
		{Name: "shot.png", Data: png},
		{Name: "report.pdf", MediaType: "application/pdf", Data: []byte("%PDF-1.4")},
		{Name: "clip.gif", MediaType: "image/gif", Data: []byte("GIF89a")},
		{Name: "note.ogg", MediaType: "audio/ogg; codecs=opus", Data: []byte("OggS")},
	}
	for _, f := range files {
		if err := ch.SendFile(t.Context(), chat, f); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range []string{"sendPhoto", "sendDocument", "sendAnimation", "sendVoice"} {
		calls := api.take(want)
		if len(calls) != 1 || calls[0].file != files[i].Name || calls[0].data != string(files[i].Data) ||
			calls[0].params["chat_id"] != "42" || calls[0].params["message_thread_id"] != "7" {
			t.Fatalf("%s = %+v", want, calls)
		}
	}
	if err := ch.SendFile(t.Context(), chat, dservice.ChannelFile{Name: "empty"}); err == nil {
		t.Fatal("an empty file must be refused")
	}
}

// Inline queries are answered with the app's mentions; a pick sent through
// the bot is marked inline and passes a mention-only group.
func TestTelegramInlineMode(t *testing.T) {
	api := &botAPI{}
	in := &inbox{mentions: []dservice.ChannelMention{{Kind: "conversation", ID: "c_1", Token: "@release", Title: "Release prep"}}}
	connected(t, api, in)
	group := tgChat{ID: -100, Type: "supergroup", Title: "Team"}
	bot := &tgUser{ID: 999, IsBot: true, Username: "nusa_bot"}
	api.mu.Lock()
	api.queue = []tgUpdate{
		{UpdateID: 1, InlineQuery: &tgInlineQuery{ID: "q1", From: &tgUser{ID: 42}, Query: "rel  check it"}},
		{UpdateID: 2, InlineQuery: &tgInlineQuery{ID: "q2", From: &tgUser{ID: 7}, Query: "rel"}}, // not allowlisted
		{UpdateID: 3, Message: &tgMessage{MessageID: 3, Chat: group, From: &tgUser{ID: 42, FirstName: "Ana"}, Date: time.Now().Unix(),
			Text: "@release  check it", ViaBot: bot}},
	}
	api.mu.Unlock()
	waitFor(t, func() bool { return len(in.messages()) == 1 })
	if m := in.messages()[0]; !m.Inline || m.Text != "@release  check it" || !m.Group {
		t.Fatalf("inline pick = %+v", m)
	}
	answers := api.take("answerInlineQuery")
	if len(answers) != 2 {
		t.Fatalf("answers = %+v", answers)
	}
	results, _ := json.Marshal(answers[0].params["results"])
	if !strings.Contains(string(results), `"message_text":"@release  check it"`) || !strings.Contains(string(results), `"id":"conversation:c_1"`) {
		t.Fatalf("results = %s", results)
	}
	if r, _ := answers[1].params["results"].([]any); len(r) != 0 {
		t.Fatalf("an unallowed sender got %v", r)
	}
	if len(in.queries) != 1 || in.queries[0] != "rel" {
		t.Fatalf("queries = %q", in.queries)
	}
}

// A chat's menu is the channel's commands plus the agent's that Telegram
// accepts; a repeat costs no call, and none puts the default menu back.
func TestTelegramCommandMenu(t *testing.T) {
	api := &botAPI{}
	ch := connected(t, api, &inbox{})
	api.take("setMyCommands") // the default menu, published on connect
	chat, _ := valueobject.NewChatRef("telegram", "-100", "5")
	cmds := []dservice.AgentCommand{{Name: "review", Description: "Review the diff\nmore"}, {Name: "Bad-Name"}, {Name: "help"}, {Name: "ok"}}
	ch.SyncCommands(t.Context(), chat, cmds)
	ch.SyncCommands(t.Context(), chat, cmds)
	set := api.take("setMyCommands")
	if len(set) != 1 {
		t.Fatalf("setMyCommands = %+v", set)
	}
	raw, _ := json.Marshal(set[0].params)
	if !strings.Contains(string(raw), `"chat_id":-100`) || !strings.Contains(string(raw), `{"command":"review","description":"Review the diff"}`) ||
		strings.Contains(string(raw), "Bad-Name") || strings.Count(string(raw), `"help"`) != 1 || strings.Contains(string(raw), `"ok"`) {
		t.Fatalf("menu = %s", raw)
	}
	ch.SyncCommands(t.Context(), chat, nil)
	if del := api.take("deleteMyCommands"); len(del) != 1 {
		t.Fatalf("deleteMyCommands = %+v", del)
	}
}
