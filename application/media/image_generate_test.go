package media

import (
	"testing"
)

func TestParseGenerateImageArgs(t *testing.T) {
	got, err := parseGenerateImageArgs(`{"prompt":"a lighthouse","n":9,"size":"1024x1024"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.N != 4 {
		t.Fatalf("n clamp = %d, want 4", got.N)
	}
	if got.Size != "1024x1024" {
		t.Fatalf("size = %q", got.Size)
	}

	if _, err := parseGenerateImageArgs(`{}`); err == nil {
		t.Fatal("expected prompt required")
	}
	if _, err := parseGenerateImageArgs(`{"prompt":"x","size":"512x512"}`); err == nil {
		t.Fatal("expected invalid size")
	}
	if _, err := parseGenerateImageArgs(`{"prompt":"x","referenced_image_paths":["rel.png"]}`); err == nil {
		t.Fatal("expected absolute path error")
	}
	if _, err := parseGenerateImageArgs(`{"prompt":"x","referenced_image_paths":["/a","/b","/c","/d","/e","/f"]}`); err == nil {
		t.Fatal("expected max paths error")
	}
}
