package generatedmedia

import (
	"testing"
)

type stubStore struct{ written [][]byte }

func (s *stubStore) WriteBytes(conversationID, name string, data []byte) (string, error) {
	s.written = append(s.written, data)
	return "/tmp/" + conversationID + "/" + name, nil
}

var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 1, 2, 3}

func mp4Magic() []byte {
	b := make([]byte, 24)
	copy(b[4:12], []byte("ftypisom"))
	return b
}

func TestSaveAcceptsValidPNG(t *testing.T) {
	att, path, err := Save(&stubStore{}, "c1", "gen-tc1", "image", pngMagic, true)
	if err != nil {
		t.Fatal(err)
	}
	if att.MediaType != "image/png" || att.Type != "image" || path == "" {
		t.Fatalf("unexpected attachment %+v path %q", att, path)
	}
	if len(att.DataURL) == 0 {
		t.Error("expected data URL")
	}
}

func TestSaveRejectsMislabeledBytes(t *testing.T) {
	if _, _, err := Save(&stubStore{}, "c1", "gen-x", "image", []byte("this is definitely not an image"), true); err == nil {
		t.Fatal("non-media bytes must be rejected")
	}
	if _, _, err := Save(&stubStore{}, "c1", "gen-x", "image", mp4Magic(), true); err == nil {
		t.Fatal("mp4 bytes must be rejected for kind=image")
	}
}

func TestSaveAcceptsMP4AsVideo(t *testing.T) {
	att, _, err := Save(&stubStore{}, "c1", "gen-vid", "video", mp4Magic(), true)
	if err != nil {
		t.Fatal(err)
	}
	if att.MediaType != "video/mp4" || att.Name[len(att.Name)-4:] != ".mp4" {
		t.Fatalf("unexpected attachment %+v", att)
	}
}

func TestSaveRejectsEmptyAndUnknownKind(t *testing.T) {
	if _, _, err := Save(&stubStore{}, "c1", "gen-x", "video", nil, true); err == nil {
		t.Fatal("empty data must be rejected")
	}
	if _, _, err := Save(&stubStore{}, "c1", "gen-x", "hologram", pngMagic, true); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
}

func TestSaveCapEnforced(t *testing.T) {
	old := Caps["image"]
	Caps["image"] = 8
	defer func() { Caps["image"] = old }()
	if _, _, err := Save(&stubStore{}, "c1", "gen-x", "image", pngMagic, true); err == nil {
		t.Fatal("over-cap data must be rejected")
	}
}

func TestSaveNilStoreRejected(t *testing.T) {
	if _, _, err := Save(nil, "c1", "gen-x", "image", pngMagic, true); err == nil {
		t.Fatal("nil store must be rejected")
	}
}

func TestSaveInlineFalseLeavesDataURLEmpty(t *testing.T) {
	att, _, err := Save(&stubStore{}, "c1", "gen-tc1", "image", pngMagic, false)
	if err != nil {
		t.Fatal(err)
	}
	if att.DataURL != "" {
		t.Errorf("expected empty DataURL for inline=false, got %q", att.DataURL[:30])
	}
	if att.FilePath == "" {
		t.Error("expected FilePath to be set")
	}
}

func TestSanitizeFilePart(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc123", "abc123"},
		{"a b/c", "a_b_c"},
		{"tool-call.1", "tool-call.1"},
		{"", ""},
		{"héllo", "héllo"}, // unicode letters are preserved
	}
	for _, tc := range cases {
		if got := SanitizeFilePart(tc.in); got != tc.want {
			t.Errorf("SanitizeFilePart(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExtForSniffedMedia(t *testing.T) {
	cases := []struct{ in, want string }{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"audio/mpeg", ".mp3"},
		{"video/mp4", ".mp4"},
		{"application/octet-stream", ".bin"},
	}
	for _, tc := range cases {
		if got := ExtForSniffedMedia(tc.in); got != tc.want {
			t.Errorf("ExtForSniffedMedia(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
