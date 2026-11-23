//go:build !windows && !darwin

package notify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// unixSender delivers with notify-send when present, otherwise with gdbus
// calling org.freedesktop.Notifications.Notify directly — minimal desktops
// often ship gdbus but not libnotify.
type unixSender struct {
	bin, kind, app, icon string
	state                *unixState
}

// unixState is a delivery breaker: a failed send (no notification daemon,
// dead bus) marks the backend down for a cooldown so later events do not
// keep spawning doomed processes. It heals by itself after the cooldown.
type unixState struct {
	lastFail atomic.Int64
	lastErr  atomic.Value // string
}

const unixFailCooldown = 60 * time.Second

func platformSender(app, icon string) dservice.Notifier {
	state := &unixState{}
	if bin, err := exec.LookPath("notify-send"); err == nil {
		return unixSender{bin: bin, kind: "notify-send", app: app, icon: icon, state: state}
	}
	if bin, err := exec.LookPath("gdbus"); err == nil {
		return unixSender{bin: bin, kind: "gdbus", app: app, icon: icon, state: state}
	}
	return none{reason: "no notifier found (install libnotify-bin or glib's gdbus)"}
}

func (s unixSender) Name() string { return s.kind }

// Available needs a session bus — both backends talk to the notification
// daemon over it — and no tripped breaker. A headless host (server, SBC)
// may have the binaries while every call would fail.
func (s unixSender) Available() bool {
	if !sessionBusReachable() {
		return false
	}
	f := s.state.lastFail.Load()
	return f == 0 || time.Since(time.Unix(0, f)) >= unixFailCooldown
}

func (s unixSender) Reason() string {
	if !sessionBusReachable() {
		return "no desktop session on this machine"
	}
	if e, _ := s.state.lastErr.Load().(string); e != "" && !s.Available() {
		return e
	}
	return ""
}

func (s unixSender) Notify(ctx context.Context, n dservice.Notification) error {
	title := n.Title
	if strings.TrimSpace(title) == "" {
		title = s.app
	}
	icon := s.icon
	if n.Icon != "" {
		icon = n.Icon
	}
	var args []string
	if s.kind == "gdbus" {
		args = s.gdbusArgs(title, n.Body, n.Urgency, icon)
	} else {
		args = s.notifySendArgs(title, n.Body, n.Urgency, icon)
	}
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Env = sessionBusEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		werr := fmt.Errorf("%s: %w (%s)", s.kind, err, strings.TrimSpace(string(out)))
		msg := werr.Error()
		if len(msg) > 160 {
			msg = msg[:160]
		}
		s.state.lastFail.Store(time.Now().UnixNano())
		s.state.lastErr.Store(msg)
		return werr
	}
	s.state.lastFail.Store(0)
	return nil
}

func (s unixSender) notifySendArgs(title, body string, urg dservice.Urgency, icon string) []string {
	u := string(urg)
	if u == "" {
		u = string(dservice.UrgencyNormal)
	}
	args := []string{"--app-name=" + s.app, "--urgency=" + u}
	if icon != "" {
		args = append(args, "--icon="+icon)
	}
	if urg == dservice.UrgencyCritical {
		args = append(args, "--expire-time=0") // stay until dismissed
	}
	return append(args, "--", title, body)
}

// gdbusArgs builds Notify(app_name, replaces_id, icon, summary, body,
// actions, hints, expire). The urgency hint is a byte: 0 low, 1 normal,
// 2 critical.
func (s unixSender) gdbusArgs(title, body string, urg dservice.Urgency, icon string) []string {
	// An explicit timeout: gdbus would parse a bare "-1" as a flag.
	u, expire := "1", "5000"
	switch urg {
	case dservice.UrgencyLow:
		u = "0"
	case dservice.UrgencyCritical:
		u, expire = "2", "0"
	}
	return []string{
		"call", "--session",
		"--dest", "org.freedesktop.Notifications",
		"--object-path", "/org/freedesktop/Notifications",
		"--method", "org.freedesktop.Notifications.Notify",
		// A title or body starting with "-" must not be read as a flag.
		"--",
		s.app, "0", icon, title, body, "[]",
		"{'urgency': <byte " + u + ">}", expire,
	}
}

// sessionBusReachable reports whether a session bus is advertised or its
// standard per-user socket exists.
func sessionBusReachable() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
		return true
	}
	_, err := os.Stat("/run/user/" + strconv.Itoa(os.Getuid()) + "/bus")
	return err == nil
}

// sessionBusEnv falls back to the per-user bus socket when a service
// environment lacks the address, so a systemd user service still reaches
// the desktop session.
func sessionBusEnv() []string {
	env := os.Environ()
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		sock := "/run/user/" + strconv.Itoa(os.Getuid()) + "/bus"
		if _, err := os.Stat(sock); err == nil {
			env = append(env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+sock)
		}
	}
	return env
}
