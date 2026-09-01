package duration

import (
	"testing"
	"time"
)

func TestParse_empty(t *testing.T) {
	got, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("empty string: got %v want 0", got)
	}
}

func TestParse_standard(t *testing.T) {
	got, err := Parse("1h30m")
	if err != nil {
		t.Fatal(err)
	}
	want := 90 * time.Minute
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParse_daySuffix(t *testing.T) {
	got, err := Parse("1d")
	if err != nil {
		t.Fatal(err)
	}
	want := 24 * time.Hour
	if got != want {
		t.Fatalf("1d: got %v want %v", got, want)
	}
}

func TestParse_dayAndHours(t *testing.T) {
	got, err := Parse("2d")
	if err != nil {
		t.Fatal(err)
	}
	want := 48 * time.Hour
	if got != want {
		t.Fatalf("2d: got %v want %v", got, want)
	}
}

func TestParse_whitespace(t *testing.T) {
	got, err := Parse("  5m  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != 5*time.Minute {
		t.Fatalf("got %v want 5m", got)
	}
}

func TestParse_invalid(t *testing.T) {
	if _, err := Parse("abc"); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}
