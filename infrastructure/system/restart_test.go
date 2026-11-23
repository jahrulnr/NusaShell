//go:build linux

package system

import (
	"testing"
)

func TestSystemdRestartQueuesWithoutBlocking(t *testing.T) {
	calls := scriptSystemd(t, true, true)
	if err := (systemd{}).Restart("nusashell"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	want := "systemctl --user --no-block restart nusashell.service"
	if len(*calls) == 0 || (*calls)[len(*calls)-1] != want {
		t.Fatalf("calls = %v, want last %q", *calls, want)
	}
}

func TestSystemdRestartRejectsBadName(t *testing.T) {
	calls := scriptSystemd(t, true, true)
	if err := (systemd{}).Restart("-x"); err == nil {
		t.Fatal("a flag-like name reached systemctl")
	}
	if len(*calls) != 0 {
		t.Fatalf("bad name touched the manager: %v", *calls)
	}
}
