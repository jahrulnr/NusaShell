package domain

import (
	"strings"
	"testing"
)

func TestSniffMagicImageFormats(t *testing.T) {
	cases := []struct {
		name     string
		prefix   string
		wantType string
		wantKind string
	}{
		{"JPEG", "\xff\xd8\xff\xe0", "image/jpeg", "image"},
		{"JPEG with SOF2", "\xff\xd8\xff\xe2", "image/jpeg", "image"},
		{"PNG", "\x89PNG\r\n\x1a\n", "image/png", "image"},
		{"GIF87a", "GIF87a", "image/gif", "image"},
		{"GIF89a", "GIF89a", "image/gif", "image"},
		{"WebP", "RIFF\x00\x00\x00\x00WEBP", "image/webp", "image"},
		{"BMP", "BM\x00\x00", "image/bmp", "image"},
		{"TIFF little-endian", "II\x2a\x00", "image/tiff", "image"},
		{"TIFF big-endian", "MM\x00\x2a", "image/tiff", "image"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := []byte(c.prefix + strings.Repeat("\x00", 64))
			gotType, gotKind := SniffMagic(data)
			if gotType != c.wantType {
				t.Errorf("SniffMagic(%s) type = %q, want %q", c.name, gotType, c.wantType)
			}
			if gotKind != c.wantKind {
				t.Errorf("SniffMagic(%s) kind = %q, want %q", c.name, gotKind, c.wantKind)
			}
		})
	}
}

func TestSniffMagicAudioFormats(t *testing.T) {
	cases := []struct {
		name     string
		prefix   string
		wantType string
		wantKind string
	}{
		{"MP3 ID3", "ID3\x03\x00", "audio/mpeg", "audio"},
		{"MP3 frame FFFB", "\xff\xfb", "audio/mpeg", "audio"},
		{"MP3 frame FFF3", "\xff\xf3", "audio/mpeg", "audio"},
		{"WAV", "RIFF\x00\x00\x00\x00WAVE", "audio/wav", "audio"},
		{"OGG", "OggS\x00", "audio/ogg", "audio"},
		{"FLAC", "fLaC", "audio/flac", "audio"},
		{"AAC ADTS", "\xff\xf1", "audio/aac", "audio"},
		{"M4A", "\x00\x00\x00\x20ftypM4A ", "audio/mp4", "audio"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := []byte(c.prefix + strings.Repeat("\x00", 64))
			gotType, gotKind := SniffMagic(data)
			if gotType != c.wantType {
				t.Errorf("SniffMagic(%s) type = %q, want %q", c.name, gotType, c.wantType)
			}
			if gotKind != c.wantKind {
				t.Errorf("SniffMagic(%s) kind = %q, want %q", c.name, gotKind, c.wantKind)
			}
		})
	}
}

func TestSniffMagicVideoFormats(t *testing.T) {
	cases := []struct {
		name     string
		prefix   string
		wantType string
		wantKind string
	}{
		{"MP4 isom", "\x00\x00\x00\x20ftypisom", "video/mp4", "video"},
		{"MP4 mp42", "\x00\x00\x00\x18ftypmp42", "video/mp4", "video"},
		{"MOV qt", "\x00\x00\x00\x14ftypqt  ", "video/quicktime", "video"},
		{"WebM", "\x1aE\xdf\xa3\x01\x00\x00\x00\x00\x00\x00\x1fB\x82\x88webm", "video/webm", "video"},
		{"AVI", "RIFF\x00\x00\x00\x00AVI ", "video/x-msvideo", "video"},
		{"MKV", "\x1aE\xdf\xa3\x01\x00\x00\x00\x00\x00\x00\x1fB\x82\x88matroska", "video/x-matroska", "video"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := []byte(c.prefix + strings.Repeat("\x00", 64))
			gotType, gotKind := SniffMagic(data)
			if gotType != c.wantType {
				t.Errorf("SniffMagic(%s) type = %q, want %q", c.name, gotType, c.wantType)
			}
			if gotKind != c.wantKind {
				t.Errorf("SniffMagic(%s) kind = %q, want %q", c.name, gotKind, c.wantKind)
			}
		})
	}
}

func TestSniffMagicUnknownOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"too short", []byte{0xFF}},
		{"plain text", []byte("hello world this is not a media file")},
		{"JavaScript", []byte("const x = 42;\nconsole.log(x);")},
		{"JSON", []byte(`{"key":"value"}`)},
		{"random bytes", []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotType, gotKind := SniffMagic(c.data)
			if gotType != "" || gotKind != "" {
				t.Errorf("SniffMagic(%s) = (%q, %q), want empty", c.name, gotType, gotKind)
			}
		})
	}
}

// TestSniffMagicRejectsMismatchedExtension proves that a file claiming to
// be an image by extension but containing JavaScript is detected as
// unknown — the guard that prevents read_media from loading non-media.
func TestSniffMagicRejectsMismatchedExtension(t *testing.T) {
	js := []byte(`const app = () => { console.log("pretending to be PNG"); };`)
	gotType, gotKind := SniffMagic(js)
	if gotKind != "" {
		t.Errorf("JavaScript misdetected as %q media type %q — magic must reject non-media", gotKind, gotType)
	}
}

func TestSniffMagicKindMatches(t *testing.T) {
	cases := []struct {
		kind   string
		prefix string
	}{
		{"image", "\xff\xd8\xff"},
		{"audio", "ID3"},
		{"video", "\x00\x00\x00\x20ftypisom"},
		{"document", "%PDF-1.4"},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			data := []byte(c.prefix + strings.Repeat("\x00", 64))
			_, gotKind := SniffMagic(data)
			if gotKind != c.kind {
				t.Errorf("kind = %q, want %q", gotKind, c.kind)
			}
		})
	}
}

func TestSniffMagicPDFFormats(t *testing.T) {
	cases := []struct {
		name     string
		prefix   string
		wantType string
		wantKind string
	}{
		{"PDF 1.0", "%PDF-1.0", "application/pdf", "document"},
		{"PDF 1.4", "%PDF-1.4", "application/pdf", "document"},
		{"PDF 1.7", "%PDF-1.7", "application/pdf", "document"},
		{"PDF 2.0", "%PDF-2.0", "application/pdf", "document"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := []byte(c.prefix + "\n%binary garbage" + strings.Repeat("\x00", 64))
			gotType, gotKind := SniffMagic(data)
			if gotType != c.wantType {
				t.Errorf("SniffMagic(%s) type = %q, want %q", c.name, gotType, c.wantType)
			}
			if gotKind != c.wantKind {
				t.Errorf("SniffMagic(%s) kind = %q, want %q", c.name, gotKind, c.wantKind)
			}
		})
	}
}
