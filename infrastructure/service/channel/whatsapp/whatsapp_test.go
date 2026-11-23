package whatsapp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repochannel "github.com/jahrulnr/nusashell/infrastructure/repository/channel"
	repochatindex "github.com/jahrulnr/nusashell/infrastructure/repository/chatindex"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

func TestAllowlist(t *testing.T) {
	s := Settings{AllowedNumbers: []string{"+62 812-3456-7890", "62812 3456 7890"}, AllowedGroups: []string{"120363@g.us", "120363@G.US:3"}}
	if err := s.normalize(); err != nil {
		t.Fatal(err)
	}
	if len(s.AllowedNumbers) != 1 || s.AllowedNumbers[0] != "6281234567890" {
		t.Fatalf("numbers = %q", s.AllowedNumbers)
	}
	if len(s.AllowedGroups) != 1 || s.AllowedGroups[0] != "120363@g.us" {
		t.Fatalf("groups = %q", s.AllowedGroups)
	}
	if !s.allows([]string{"6281234567890@s.whatsapp.net"}, []string{"6281234567890@s.whatsapp.net"}) {
		t.Fatal("an allowed number must pass")
	}
	if !s.allows([]string{"999@s.whatsapp.net"}, []string{"120363@g.us"}) {
		t.Fatal("any member of an allowed group must pass")
	}
	if s.allows([]string{"999@s.whatsapp.net"}, []string{"999@s.whatsapp.net"}) {
		t.Fatal("an unknown sender must not pass")
	}
	if (Settings{}).allows([]string{"1"}, []string{"1"}) {
		t.Fatal("an empty allowlist must allow nobody")
	}
	for _, bad := range []Settings{{AllowedNumbers: []string{"abc"}}, {AllowedGroups: []string{"12345"}}} {
		if bad.normalize() == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
	bad := defaults()
	bad.AllowedNumbers = []string{"*", "62812"}
	_ = bad.normalize()
	if bad.validate() == nil {
		t.Fatal("* combined with numbers must be refused")
	}
}

func TestUnpairedDeviceStartsAndRefusesToSend(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ch := New(filepath.Join(dir, "whatsapp.db"), "NusaShell", repochannel.New(db), repochatindex.New(db), nil)
	if err := ch.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	defer ch.Stop()
	if st := ch.Status(); st.State != "unpaired" || st.Account != "" || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	if _, err := ch.QR(ctx); err == nil {
		t.Fatal("no QR before Connect")
	}
	chat, _ := valueobject.NewChatRef("whatsapp", "6281234567890@s.whatsapp.net", "")
	if _, err := ch.Send(ctx, chat, "hi"); err == nil {
		t.Fatal("sending without a connection must fail")
	}
	if err := ch.UpdateSettings(ctx, json.RawMessage(`{"allowedNumbers":["+62 812 3456 7890"],"groupPolicy":"all"}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := ch.Settings(ctx)
	var got Settings
	_ = json.Unmarshal(raw, &got)
	if got.GroupPolicy != "all" || len(got.AllowedNumbers) != 1 || got.AllowedNumbers[0] != "6281234567890" {
		t.Fatalf("settings = %+v", got)
	}
	if err := ch.UpdateSettings(ctx, json.RawMessage(`{"bogus":1}`)); err == nil {
		t.Fatal("unknown fields must be refused")
	}

	// Add and remove compare canonical forms; the pacing fields are kept.
	err = ch.UpdateSettings(ctx, json.RawMessage(`{"addNumbers":["+1 555 0100"],"removeNumbers":["62-812-3456-7890"],
		"addGroups":["120363@G.US"],"sendPacing":0,"queueCap":3,"retentionDays":30}`))
	if err != nil {
		t.Fatal(err)
	}
	if got = ch.current(); len(got.AllowedNumbers) != 1 || got.AllowedNumbers[0] != "15550100" || len(got.AllowedGroups) != 1 ||
		got.SendPacing != 0 || got.QueueCap != 3 || got.RetentionDays != 30 {
		t.Fatalf("after add/remove = %+v", got)
	}
	for _, bad := range []string{`{"addNumbers":["abc"]}`, `{"addGroups":["12345"]}`, `{"queueCap":0}`, `{"sendPacing":61}`,
		`{"groupPolicy":"sometimes"}`, `{"addNumbers":["*"]}`} {
		if err := ch.UpdateSettings(ctx, json.RawMessage(bad)); err == nil {
			t.Fatalf("%s must be refused", bad)
		}
	}
	if _, err := ch.SendJSON(ctx, json.RawMessage(`{"target":"6281","type":"text","text":"hi"}`)); err == nil {
		t.Fatal("an explicit send without a connection must fail")
	}
	if _, err := ch.SendJSON(ctx, json.RawMessage(`{"target":"6281","type":"text","bogus":1}`)); err == nil {
		t.Fatal("unknown send fields must be refused")
	}
}

// The pacer keeps the least gap between two sends to one chat, runs them
// in order, and refuses past the queue cap; a stop fails what waits.
func TestPacer(t *testing.T) {
	c := New("", "test", nil, nil, nil)
	c.cfg = defaults()
	c.cfg.SendPacing, c.cfg.QueueCap = 1, 2
	ctx := t.Context()
	var order []int
	start := time.Now()
	done := make(chan error, 3)
	for i := range 2 {
		go func() {
			done <- c.paced(ctx, "a", func(context.Context) error { order = append(order, i); return nil })
		}()
		time.Sleep(20 * time.Millisecond)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < time.Second || len(order) != 2 || order[0] != 0 {
		t.Fatalf("order %v in %v", order, took)
	}
	// Another chat is not held back by this one.
	if err := c.paced(ctx, "b", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	go func() { done <- c.paced(ctx, "a", func(context.Context) error { <-block; return nil }) }()
	time.Sleep(20 * time.Millisecond)
	waiting := make(chan error, 2)
	for range 2 {
		go func() { waiting <- c.paced(ctx, "a", func(context.Context) error { return nil }) }()
	}
	time.Sleep(20 * time.Millisecond)
	if err := c.paced(ctx, "a", func(context.Context) error { return nil }); err != errQueueFull {
		t.Fatalf("over the cap err = %v", err)
	}
	c.stopPacer()
	for range 2 {
		if err := <-waiting; err != errPacerStopped {
			t.Fatalf("queued after stop err = %v", err)
		}
	}
	close(block)
	<-done
}

func TestQuoteOf(t *testing.T) {
	reply := func(quoted *waE2E.Message) *waE2E.Message {
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        new("yes"),
			ContextInfo: &waE2E.ContextInfo{QuotedMessage: quoted},
		}}
	}
	text := &waE2E.Message{Conversation: new(" shall we? ")}
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"not a reply", &waE2E.Message{Conversation: new("hi")}, ""},
		{"nil", nil, ""},
		{"text", reply(text), "shall we?"},
		{"image with a caption", reply(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: new("the chart")}}), "the chart"},
		{"image without one", reply(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}), "an image"},
		{"voice note", reply(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}), "a voice message"},
		{"view-once text", reply(&waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: text}}), "shall we?"},
		{"ephemeral image", reply(&waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}}}), "an image"},
		{"unknown kind", reply(&waE2E.Message{}), "a message"},
		{"a voice note replying", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			ContextInfo: &waE2E.ContextInfo{QuotedMessage: text},
		}}, "shall we?"},
	}
	for _, c := range cases {
		if got := quoteOf(c.msg); got != c.want {
			t.Errorf("%s: quoteOf = %q, want %q", c.name, got, c.want)
		}
	}
}
