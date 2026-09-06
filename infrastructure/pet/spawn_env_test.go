package pet

import (
	"strings"
	"testing"
)

func TestPetWSURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host, port, want string
	}{
		{"127.0.0.1", "10994", "ws://127.0.0.1:10994/ws"},
		{"", "7777", "ws://127.0.0.1:7777/ws"},
		{"0.0.0.0", "10994", "ws://127.0.0.1:10994/ws"},
		{"::", "10994", "ws://127.0.0.1:10994/ws"},
		{"localhost", "80", "ws://localhost:80/ws"},
		{"127.0.0.1", "", ""},
		{"  127.0.0.1 ", " 10994 ", "ws://127.0.0.1:10994/ws"},
	}
	for _, tc := range tests {
		if got := petWSURL(tc.host, tc.port); got != tc.want {
			t.Errorf("petWSURL(%q,%q) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestPetSpawnEnvOverridesParent(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"NUSASHELL_PORT=9999",
		"NUSASHELL_HOST=old",
		"NUSASHELL_WS_URL=ws://old:9999/ws",
		"HOME=/tmp",
	}
	got := petSpawnEnv(parent, "127.0.0.1", "10994")
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/tmp") {
		t.Fatalf("parent env dropped: %v", got)
	}
	if strings.Contains(joined, "NUSASHELL_PORT=9999") || strings.Contains(joined, "ws://old:9999/ws") {
		t.Fatalf("stale backend env kept: %v", got)
	}
	if !strings.Contains(joined, "NUSASHELL_PORT=10994") {
		t.Fatalf("port missing: %v", got)
	}
	if !strings.Contains(joined, "NUSASHELL_HOST=127.0.0.1") {
		t.Fatalf("host missing: %v", got)
	}
	if !strings.Contains(joined, "NUSASHELL_WS_URL=ws://127.0.0.1:10994/ws") {
		t.Fatalf("ws url missing: %v", got)
	}
}

func TestPetSpawnEnvEmptyPortLeavesParent(t *testing.T) {
	t.Parallel()
	parent := []string{"NUSASHELL_PORT=9999"}
	got := petSpawnEnv(parent, "127.0.0.1", "")
	if len(got) != 1 || got[0] != "NUSASHELL_PORT=9999" {
		t.Fatalf("empty port must not rewrite env, got %v", got)
	}
}

func TestPetSpawnArgsIncludeWSURL(t *testing.T) {
	t.Parallel()
	got := petSpawnArgs("/opt/assets", "127.0.0.1", "10994")
	want := []string{"--assets", "/opt/assets", "--ws-url", "ws://127.0.0.1:10994/ws"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", got, want)
	}
	got = petSpawnArgs("/opt/assets", "", "")
	if len(got) != 2 || got[0] != "--assets" {
		t.Fatalf("no backend must keep assets only, got %v", got)
	}
}
