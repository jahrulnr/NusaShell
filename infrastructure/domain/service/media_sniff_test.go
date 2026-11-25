package service

import "testing"

// One rule names a payload for every caller: the bytes decide, and the
// extension settles what the byte sniffer cannot say alone. These are the
// shapes the tools meet — a voice note whose container sniffs as data, an
// MPEG-4 audio file the sniffer calls video, an Ogg stream that may be either.
func TestSniffMediaType(t *testing.T) {
	ftyp := func(brand string) []byte { return []byte("\x00\x00\x00\x18ftyp" + brand + "\x00\x00\x00\x00") }
	ogg := []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00")
	cases := []struct {
		name string
		path string
		raw  []byte
		want string
	}{
		{"m4a voice note", "voice.m4a", ftyp("M4A "), "audio/mp4"},
		{"m4a whose brand looks like mp4", "voice.m4a", ftyp("mp42isom"), "audio/mp4"},
		{"mp4 video", "clip.mp4", []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), "video/mp4"},
		{"ogg audio", "voice.ogg", ogg, "audio/ogg"},
		{"ogv video", "clip.ogv", ogg, "video/ogg"},
		{"aac stream", "voice.aac", []byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC}, "audio/aac"},
		{"flac", "voice.flac", []byte("fLaC\x00\x00\x00\x22"), "audio/flac"},
		{"mp3", "voice.mp3", []byte("ID3\x04\x00\x00\x00\x00\x00\x00"), "audio/mpeg"},
		{"opus", "voice.opus", ogg, "audio/ogg"},
		{"quicktime", "clip.mov", ftyp("qt  "), "video/quicktime"},
		{"webm video", "clip.webm", []byte("\x1a\x45\xdf\xa3\x42\x86\x81\x01"), "video/webm"},
		{"weba audio", "voice.weba", []byte("\x1a\x45\xdf\xa3\x42\x86\x81\x01"), "audio/webm"},
		{"png", "shot.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "image/png"},
		{"text", "notes.txt", []byte("hello there\n"), "text/plain"},
		{"unknown bytes", "blob", []byte{0x00, 0x01, 0x02, 0xFD, 0xFE, 0x81}, "application/octet-stream"},
	}
	for _, tc := range cases {
		if got := SniffMediaType(tc.raw, tc.path); got != tc.want {
			t.Errorf("%s (%s): SniffMediaType = %q, want %q", tc.name, tc.path, got, tc.want)
		}
	}
}
