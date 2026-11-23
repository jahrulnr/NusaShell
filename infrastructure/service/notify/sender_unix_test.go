//go:build !windows && !darwin

package notify

import (
	"slices"
	"testing"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

const testIcon = "/data/nusashell.png"

func TestNotifySendCarriesTheAppIcon(t *testing.T) {
	s := unixSender{kind: "notify-send", app: "NusaShell", icon: testIcon}
	args := s.notifySendArgs("Turn failed", "boom", dservice.UrgencyNormal, s.icon)
	if !slices.Contains(args, "--icon="+testIcon) {
		t.Fatalf("no app icon in %v", args)
	}
	for _, a := range args {
		if a == "--icon=dialog-error" || a == "--icon=dialog-information" || a == "--icon=dialog-question" {
			t.Fatalf("stock system icon %q in %v", a, args)
		}
	}
	if args[len(args)-3] != "--" || args[len(args)-2] != "Turn failed" || args[len(args)-1] != "boom" {
		t.Fatalf("title and body must follow --: %v", args)
	}
}

func TestNotificationIconOverridesTheAppIcon(t *testing.T) {
	s := unixSender{kind: "notify-send", app: "NusaShell", icon: testIcon}
	args := s.notifySendArgs("Update available", "1.2.3", dservice.UrgencyLow, "system-software-update")
	if !slices.Contains(args, "--icon=system-software-update") {
		t.Fatalf("per-notification icon missing from %v", args)
	}
	if slices.Contains(args, "--icon="+testIcon) {
		t.Fatalf("app icon overrode the notification icon: %v", args)
	}
}

func TestNotifySendWithoutAnIconLeavesItToTheSystem(t *testing.T) {
	s := unixSender{kind: "notify-send", app: "NusaShell"}
	for _, a := range s.notifySendArgs("t", "b", dservice.UrgencyCritical, s.icon) {
		if len(a) >= 6 && a[:6] == "--icon" {
			t.Fatalf("unexpected %q with no icon configured", a)
		}
	}
}

func TestGdbusCarriesTheAppIconAsAppIcon(t *testing.T) {
	s := unixSender{kind: "gdbus", app: "NusaShell", icon: testIcon}
	args := s.gdbusArgs("Turn failed", "boom", dservice.UrgencyCritical, s.icon)
	i := slices.Index(args, "--")
	if i < 0 {
		t.Fatalf("no -- before the Notify arguments: %v", args)
	}
	// Notify(app_name, replaces_id, app_icon, summary, body, actions, hints, expire)
	want := []string{"NusaShell", "0", testIcon, "Turn failed", "boom", "[]", "{'urgency': <byte 2>}", "0"}
	if got := args[i+1:]; !slices.Equal(got, want) {
		t.Fatalf("Notify arguments = %v, want %v", got, want)
	}
}

func TestGdbusNotificationIconOverridesTheAppIcon(t *testing.T) {
	s := unixSender{kind: "gdbus", app: "NusaShell", icon: testIcon}
	args := s.gdbusArgs("Update available", "1.2.3", dservice.UrgencyLow, "system-software-update")
	i := slices.Index(args, "--")
	if i < 0 {
		t.Fatalf("no -- before the Notify arguments: %v", args)
	}
	// The third Notify argument is the icon; the hint stays low urgency.
	want := []string{"NusaShell", "0", "system-software-update", "Update available", "1.2.3", "[]", "{'urgency': <byte 0>}", "5000"}
	if got := args[i+1:]; !slices.Equal(got, want) {
		t.Fatalf("Notify arguments = %v, want %v", got, want)
	}
}
