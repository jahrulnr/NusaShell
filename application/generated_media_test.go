package application

import (
	"testing"

	"nusashell/domain"
)

type stubAttach struct{ written [][]byte }

func (s *stubAttach) Save(conversationID string, att domain.Attachment) (string, error) {
	return "/tmp/" + conversationID + "/" + att.Name, nil
}
func (s *stubAttach) WriteBytes(conversationID, name string, data []byte) (string, error) {
	s.written = append(s.written, data)
	return "/tmp/" + conversationID + "/" + name, nil
}
func (s *stubAttach) ReadFile(absPath string) ([]byte, error) { return nil, nil }

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 1, 2, 3}

func mp4Magic() []byte {
	b := make([]byte, 24)
	copy(b[4:12], []byte("ftypisom"))
	return b
}

func newMediaApp() *App {
	return &App{Attachments: &stubAttach{}}
}

// TestExecuteGenerateMediaRoutesByType pins the unified generate_media
// dispatch: media_type picks the executor, and legacy names from older
// conversations fall back by tool name when media_type is absent.
func TestExecuteGenerateMediaRoutesByType(t *testing.T) {
	online := &fakeOnlineTTS{result: &TTSResult{Audio: mp3Bytes(), MediaType: "audio/mpeg", Ext: "mp3", Provider: "openai", Model: "tts-1", Voice: "alloy"}}
	app := ttsApp(t, online, nil)
	run, cancel := ttsRun(t)
	defer cancel()
	settings := domain.Settings{TTSProviderID: "ttsprov", TTSModelID: "tts-1"}

	if _, _, err := app.executeGenerateMedia(run,
		domain.ToolCall{ID: "tc1", Args: `{"media_type":"speech","prompt":"halo"}`}, settings); err != nil {
		t.Fatal(err)
	}
	if online.got.Text != "halo" {
		t.Fatalf("unified prompt not routed to speech: %q", online.got.Text)
	}

	online2 := &fakeOnlineTTS{result: &TTSResult{Audio: mp3Bytes(), MediaType: "audio/mpeg", Ext: "mp3", Voice: "alloy"}}
	app2 := ttsApp(t, online2, nil)
	if _, _, err := app2.executeGenerateMedia(run,
		domain.ToolCall{ID: "tc2", Name: "generate_speech", Args: `{"text":"halo2"}`}, settings); err != nil {
		t.Fatal(err)
	}
	if online2.got.Text != "halo2" {
		t.Fatalf("legacy alias not routed by tool name: %q", online2.got.Text)
	}
}
