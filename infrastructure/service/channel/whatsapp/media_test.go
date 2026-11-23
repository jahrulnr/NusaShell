package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

func TestPollText(t *testing.T) {
	p := &waE2E.PollCreationMessage{Name: new("Lunch?"), SelectableOptionsCount: new(uint32(2)),
		Options: []*waE2E.PollCreationMessage_Option{{OptionName: new("Rice")}, {OptionName: new(" ")}, {OptionName: new("Noodles")}}}
	if got := pollText(p); got != "[Poll] Lunch?\n- Rice\n- Noodles\n(choose up to 2)" {
		t.Fatalf("pollText = %q", got)
	}
	wrapped := &waE2E.Message{PollCreationMessageV4: &waE2E.FutureProofMessage{Message: &waE2E.Message{PollCreationMessageV3: p}}}
	if pollCreation(wrapped) != p || !isPollTraffic(wrapped) {
		t.Fatal("a wrapped poll must be found")
	}
	if isPollTraffic(&waE2E.Message{Conversation: new("hi")}) {
		t.Fatal("text is not poll traffic")
	}
}

// A poll's options are kept under the device-free chat JID too, so a vote
// whose key names another device still reads back; an added option joins.
func TestPollOptionsCache(t *testing.T) {
	c := New("", "test", nil, nil, nil)
	c.rememberPoll("628123:3@s.whatsapp.net", "P1", []string{"Yes", "No"})
	key := &waCommon.MessageKey{RemoteJID: new("628123@s.whatsapp.net"), ID: new("P1")}
	if got := c.pollOptions(key); len(got) != 2 || got[0] != "Yes" {
		t.Fatalf("options = %q", got)
	}
	add := &waE2E.PollAddOptionMessage{PollCreationMessageKey: key, AddOption: &waE2E.PollCreationMessage_Option{OptionName: new("Maybe")}}
	if got := c.rememberPollOption(add); got != "[Poll option added: Maybe]" {
		t.Fatalf("added = %q", got)
	}
	if got := c.pollOptions(key); len(got) != 3 || got[2] != "Maybe" {
		t.Fatalf("options after add = %q", got)
	}
	for i := range pollCacheLimit + 10 {
		c.rememberPoll("1@g.us", string(rune('a'+i%26))+string(rune(i)), []string{"x", "y"})
	}
	if len(c.polls) > pollCacheLimit {
		t.Fatalf("cache grew to %d", len(c.polls))
	}
}

func TestFileMessageKinds(t *testing.T) {
	up := whatsmeow.UploadResponse{URL: "u", DirectPath: "/d", FileLength: 4}
	if m := fileMessage(up, whatsmeow.MediaImage, "image/png", "a.png", "look"); m.GetImageMessage().GetMimetype() != "image/png" || m.GetImageMessage().GetCaption() != "look" {
		t.Fatalf("image = %v", m)
	}
	if m := fileMessage(up, whatsmeow.MediaAudio, "audio/ogg", "v.ogg", ""); !m.GetAudioMessage().GetPTT() {
		t.Fatal("ogg audio must be a voice note")
	}
	if m := fileMessage(up, whatsmeow.MediaAudio, "audio/mpeg", "s.mp3", ""); m.GetAudioMessage().GetPTT() {
		t.Fatal("mp3 audio is not a voice note")
	}
	if m := fileMessage(up, whatsmeow.MediaDocument, "application/pdf", "../x/report.pdf", ""); m.GetDocumentMessage().GetFileName() != "report.pdf" {
		t.Fatalf("document = %v", m)
	}
	if m := fileMessage(up, whatsmeow.MediaDocument, "text/plain", "", ""); m.GetDocumentMessage().GetFileName() != "file" {
		t.Fatalf("unnamed document = %v", m)
	}
	if extensionFor("audio/opus", ".ogg") != ".opus" || extensionFor("application/x-thing", ".bin") != ".bin" {
		t.Fatal("extensionFor")
	}
	if joinText("a", "a") != "a" || joinText("", " b ") != "b" || joinText("a", "b") != "a\n\nb" {
		t.Fatal("joinText")
	}
}
