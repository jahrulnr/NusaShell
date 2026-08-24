package domain

import (
	"strings"
	"testing"
)

func TestVisionImagePathNote(t *testing.T) {
	atts := []Attachment{
		{Type: "image", Name: "cat.png", FilePath: "/data/attachments/c1/cat.png"},
		{Type: "text", Name: "note.txt", Content: "hi"},
	}
	note := VisionImagePathNote(atts)
	if note == "" {
		t.Fatal("expected a non-empty note")
	}
	if !strings.Contains(note, "/data/attachments/c1/cat.png") {
		t.Errorf("note should include the absolute file path, got %q", note)
	}
	if !strings.Contains(note, "referenced_image_paths") {
		t.Errorf("note should mention referenced_image_paths, got %q", note)
	}
	// The note must not resemble the non-vision omission placeholder, or a
	// vision model could wrongly conclude the image was stripped.
	if strings.Contains(note, "content omitted") {
		t.Errorf("vision note must not resemble the omission placeholder, got %q", note)
	}
}

func TestVisionImagePathNoteMultiple(t *testing.T) {
	atts := []Attachment{
		{Type: "image", Name: "a.png", FilePath: "/x/a.png"},
		{Type: "image", Name: "b.png", FilePath: "/x/b.png"},
	}
	note := VisionImagePathNote(atts)
	if !strings.Contains(note, "/x/a.png") || !strings.Contains(note, "/x/b.png") {
		t.Errorf("note should list every image path, got %q", note)
	}
}

func TestVisionImagePathNoteEmptyWithoutPath(t *testing.T) {
	// Image with no FilePath yields no note (nothing to reference).
	if note := VisionImagePathNote([]Attachment{{Type: "image", Name: "cat.png"}}); note != "" {
		t.Errorf("expected empty note when no file path, got %q", note)
	}
	if note := VisionImagePathNote(nil); note != "" {
		t.Errorf("expected empty note for nil attachments, got %q", note)
	}
	// Non-image attachments are ignored.
	if note := VisionImagePathNote([]Attachment{{Type: "file", Name: "d.pdf", FilePath: "/x/d.pdf"}}); note != "" {
		t.Errorf("expected empty note for non-image attachments, got %q", note)
	}
}

func TestContainsVisionImageNote(t *testing.T) {
	note := VisionImagePathNote([]Attachment{{Type: "image", FilePath: "/x/y.png"}})
	if !ContainsVisionImageNote(note) {
		t.Error("ContainsVisionImageNote should detect the generated note")
	}
	if ContainsVisionImageNote("plain text") {
		t.Error("ContainsVisionImageNote should be false for plain text")
	}
	if ContainsVisionImageNote("[image content omitted — this model does not support image input]") {
		t.Error("ContainsVisionImageNote must not match the omission placeholder")
	}
}

func TestOmittedPlaceholderIncludesImageEditHint(t *testing.T) {
	atts := []Attachment{{Type: "image", Name: "cat.png", FilePath: "/x/cat.png"}}
	note := OmittedPlaceholderFor("image", "read_media", atts)
	if !strings.Contains(note, "referenced_image_paths") || !strings.Contains(note, "generate_image") {
		t.Errorf("image omission placeholder should include the generate_image i2i hint, got %q", note)
	}
	if !strings.Contains(note, "read_media") {
		t.Errorf("image omission placeholder should still mention read_media, got %q", note)
	}
	// Audio/video placeholders stay free of the image-only hint.
	for _, kind := range []string{"audio", "video"} {
		p := OmittedPlaceholderFor(kind, "read_media", []Attachment{{Type: kind, FilePath: "/x/f"}})
		if strings.Contains(p, "generate_image") {
			t.Errorf("%s placeholder must not contain the image-only i2i hint, got %q", kind, p)
		}
	}
}
