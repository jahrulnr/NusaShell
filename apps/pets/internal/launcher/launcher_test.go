package launcher

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestChooseClickPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		st   Status
		want Action
	}{
		{
			name: "nothing running anywhere",
			st:   Status{GoRunning: false, ElectronInstalled: true, ElectronRunning: false},
			want: ActionNone, // must not start golang nor electron
		},
		{
			name: "electron installed and running, backend down",
			st:   Status{GoRunning: false, ElectronInstalled: true, ElectronRunning: true},
			want: ActionOpenElectron, // focus the running wrapper
		},
		{
			name: "electron installed but not running, backend up",
			st:   Status{GoRunning: true, ElectronInstalled: true, ElectronRunning: false},
			want: ActionOpenElectron, // electron first, not the web fallback
		},
		{
			name: "electron installed and running, backend up",
			st:   Status{GoRunning: true, ElectronInstalled: true, ElectronRunning: true},
			want: ActionOpenElectron, // focus over relaunching
		},
		{
			name: "electron running, binary unresolvable, backend up",
			st:   Status{GoRunning: true, ElectronInstalled: false, ElectronRunning: true},
			want: ActionOpenElectron,
		},
		{
			name: "no electron, backend up",
			st:   Status{GoRunning: true, ElectronInstalled: false, ElectronRunning: false},
			want: ActionOpenWeb, // fallback to web when electron is not installed
		},
		{
			name: "nothing installed or running",
			st:   Status{},
			want: ActionNone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Choose(tc.st); got != tc.want {
				t.Fatalf("Choose(%+v) = %v, want %v", tc.st, got, tc.want)
			}
		})
	}
}

func TestWebURLDerivedFromWebSocketURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		wsURL string
		want  string
	}{
		{"ws://127.0.0.1:9999/ws", "http://127.0.0.1:9999/"},
		{"wss://host.example:8443/ws/x", "https://host.example:8443/"},
		{"", DefaultWebURL},
		{"garbage", DefaultWebURL},
	}
	for _, tc := range tests {
		if got := WebURL(tc.wsURL); got != tc.want {
			t.Fatalf("WebURL(%q) = %q, want %q", tc.wsURL, got, tc.want)
		}
	}
}

func TestSpawnDetached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "spawned.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho $$ > \""+marker+"\"\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Spawn(script); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	pid := 0
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("spawned process did not start")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		t.Fatalf("cleanup kill: %v", err)
	}
}

func TestSpawnMissingBinary(t *testing.T) {
	t.Parallel()
	if err := Spawn(filepath.Join(t.TempDir(), "no-such-binary")); err == nil {
		t.Fatal("Spawn must error for a missing binary")
	}
}
