package pet

import (
	"strings"
	"testing"
)

func TestParseSystemdShowEnvironment(t *testing.T) {
	t.Parallel()
	got := parseSystemdShowEnvironment("HOME=/home/u\nDISPLAY=:1\nXAUTHORITY=/tmp/xauth\nDBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1/bus\n")
	if got["DISPLAY"] != ":1" || got["XAUTHORITY"] != "/tmp/xauth" {
		t.Fatalf("got %#v", got)
	}
	if _, ok := got["HOME"]; ok {
		t.Fatal("HOME must not be copied from systemd env")
	}
}

func TestEnrichGraphicalEnvFillsMissingDisplay(t *testing.T) {
	t.Parallel()
	parent := []string{"HOME=/home/u", "PATH=/bin"}
	got := enrichGraphicalEnv(parent, func() map[string]string {
		return map[string]string{"DISPLAY": ":0", "XAUTHORITY": "/run/user/1000/gdm/Xauthority"}
	})
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "DISPLAY=:0") {
		t.Fatalf("DISPLAY not merged: %v", got)
	}
	if !strings.Contains(joined, "XAUTHORITY=/run/user/1000/gdm/Xauthority") {
		t.Fatalf("XAUTHORITY not merged: %v", got)
	}
}

func TestEnrichGraphicalEnvKeepsExistingDisplay(t *testing.T) {
	t.Parallel()
	parent := []string{"DISPLAY=:2", "HOME=/home/u"}
	got := enrichGraphicalEnv(parent, func() map[string]string {
		return map[string]string{"DISPLAY": ":9", "XAUTHORITY": "/tmp/x"}
	})
	if envValue(got, "DISPLAY") != ":2" {
		t.Fatalf("existing DISPLAY must win, got %v", got)
	}
	if envValue(got, "XAUTHORITY") != "" {
		t.Fatalf("must not merge extras when DISPLAY already set, got %v", got)
	}
}

func TestMergeEnvIfMissing(t *testing.T) {
	t.Parallel()
	got := mergeEnvIfMissing([]string{"PATH=/bin"}, map[string]string{"DISPLAY": ":1", "XAUTHORITY": "/x"})
	if envValue(got, "DISPLAY") != ":1" || envValue(got, "XAUTHORITY") != "/x" {
		t.Fatalf("got %v", got)
	}
}
