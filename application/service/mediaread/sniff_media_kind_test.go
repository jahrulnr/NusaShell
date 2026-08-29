package mediaread

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSniffMediaKindFromArgs(t *testing.T) {
	dir := t.TempDir()

	// Real PNG magic bytes.
	pngPath := filepath.Join(dir, "photo.bin")
	if err := os.WriteFile(pngPath, realPNGHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	// Real MP3 magic bytes (ID3 tag).
	mp3Path := filepath.Join(dir, "song.dat")
	if err := os.WriteFile(mp3Path, realMP3Header, 0o644); err != nil {
		t.Fatal(err)
	}
	// Real MP4 magic bytes.
	mp4Path := filepath.Join(dir, "clip.unknown")
	if err := os.WriteFile(mp4Path, realMP4Header, 0o644); err != nil {
		t.Fatal(err)
	}
	// Real PDF magic bytes.
	pdfPath := filepath.Join(dir, "report.dat")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4\n%binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-media file (JavaScript).
	jsPath := filepath.Join(dir, "script.js")
	if err := os.WriteFile(jsPath, []byte("alert('hi')"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{"png via .bin extension", pngPath, "image"},
		{"mp3 via .dat extension", mp3Path, "audio"},
		{"mp4 via .unknown extension", mp4Path, "video"},
		{"pdf via .dat extension", pdfPath, "document"},
		{"non-media js", jsPath, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"file_path": tc.path})
			got, err := SniffMediaKind(args)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("expected error for non-media file, got kind %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSniffMediaKindMissingPath(t *testing.T) {
	args, _ := json.Marshal(map[string]string{})
	_, err := SniffMediaKind(args)
	if err == nil {
		t.Fatal("expected error for missing file_path")
	}
}

func TestSniffMediaKindFileNotFound(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"file_path": "/nonexistent/path/file.png"})
	_, err := SniffMediaKind(args)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}
