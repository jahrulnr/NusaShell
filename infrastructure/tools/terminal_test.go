package tools

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{512, "512"},
		{1023, "1023"},
		{1024, "1.0K"},
		{4096, "4.0K"},
		{13312, "13K"},
		{1048576, "1.0M"},
		{33554432, "32M"},
		{1073741824, "1.0G"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLsTime(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-24 * time.Hour)
	old := now.Add(-400 * 24 * time.Hour)
	if got := lsTime(recent, now); got != "Aug 26 12:00" {
		t.Errorf("recent = %q, want %q", got, "Aug 26 12:00")
	}
	if got := lsTime(old, now); !strings.Contains(got, "2025") {
		t.Errorf("old file should show year, got %q", got)
	}
}

func TestLsLine(t *testing.T) {
	dir := t.TempDir()
	f := dir + "/hello.txt"
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	line := lsLine(info, time.Now())
	if !strings.Contains(line, "hello.txt") || !strings.Contains(line, "2") {
		t.Errorf("lsLine missing name/size: %q", line)
	}
	if !strings.HasPrefix(line, "-") {
		t.Errorf("regular file should start with mode '-', got %q", line)
	}
}
