package domain

import "testing"

func TestMediaDescPrefixConstants(t *testing.T) {
	if MediaDescPrefixVision != "vision:" {
		t.Errorf("MediaDescPrefixVision = %q, want %q", MediaDescPrefixVision, "vision:")
	}
	if MediaDescPrefixAudio != "audio:" {
		t.Errorf("MediaDescPrefixAudio = %q, want %q", MediaDescPrefixAudio, "audio:")
	}
	if MediaDescPrefixVideo != "video:" {
		t.Errorf("MediaDescPrefixVideo = %q, want %q", MediaDescPrefixVideo, "video:")
	}
}

func TestHasMediaDescription(t *testing.T) {
	atts := []Attachment{
		{Type: "image", Name: "cat.png"},
		{Type: "text", Name: "vision:cat.png"},
		{Type: "audio", Name: "song.mp3"},
	}
	if !HasMediaDescription(atts, MediaDescPrefixVision, "cat.png") {
		t.Error("expected vision:cat.png to be present")
	}
	if HasMediaDescription(atts, MediaDescPrefixVision, "dog.png") {
		t.Error("vision:dog.png should not be present")
	}
	if HasMediaDescription(atts, MediaDescPrefixAudio, "song.mp3") {
		t.Error("audio:song.mp3 should not be present (only image desc exists)")
	}
	if HasMediaDescription(nil, MediaDescPrefixVision, "x.png") {
		t.Error("nil atts should return false")
	}
}

func TestUndescribedMediaIndexes(t *testing.T) {
	atts := []Attachment{
		{Type: "text", Name: "intro.md"},
		{Type: "image", Name: "b.png"},
		{Type: "image", Name: "c.png"},
		{Type: "text", Name: "vision:b.png"},
		{Type: "audio", Name: "song.mp3"},
	}
	got := UndescribedMediaIndexes(atts, "image", MediaDescPrefixVision)
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("undescribed images = %v, want [2]", got)
	}
	if n := UndescribedMediaIndexes(atts, "audio", MediaDescPrefixAudio); len(n) != 1 || n[0] != 4 {
		t.Fatalf("undescribed audio = %v, want [4]", n)
	}
	if n := UndescribedMediaIndexes(nil, "image", MediaDescPrefixVision); len(n) != 0 {
		t.Fatalf("empty attachments = %v, want []", n)
	}
}
