// Package appnotify decides which happenings deserve a desktop
// notification on the machine NusaShell runs on: a web turn that ended, a
// permission request, an automation run, a newer release. Showing one is
// the Notifier's job.
package appnotify

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Rooms reads thread titles.
type Rooms interface {
	Info(ctx context.Context, id string) (drepo.ConversationInfo, error)
}

// deliveryTimeout leaves the platform backend time to start.
const deliveryTimeout = 20 * time.Second

// App sends desktop notifications.
type App struct {
	sender  dservice.Notifier
	enabled func() bool
	rooms   Rooms
	app     string
	log     *slog.Logger
	muted   atomic.Bool
}

// New wires the use case. enabled reads the owner's setting each time, so
// a change applies without a restart.
func New(sender dservice.Notifier, enabled func() bool, rooms Rooms, appName string, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	a := &App{sender: sender, enabled: enabled, rooms: rooms, app: appName, log: log.With("component", "notify")}
	if !sender.Available() {
		a.log.Info("desktop notifications unavailable", "backend", sender.Name(), "reason", sender.Reason())
	}
	return a
}

// Status is the notification state for the settings page.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Backend   string `json:"backend"`
	Reason    string `json:"reason,omitempty"`
}

// Status reports whether notifications are on and can be shown here.
func (a *App) Status() Status {
	return Status{Enabled: a.enabled(), Available: a.sender.Available(), Backend: a.sender.Name(), Reason: a.sender.Reason()}
}

// Test shows a sample notification, whatever the setting says.
func (a *App) Test(ctx context.Context) error {
	if !a.sender.Available() {
		return apperror.New(409, "desktop notifications are not available here: "+a.sender.Reason(), nil)
	}
	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	if err := a.sender.Notify(ctx, dservice.Notification{Title: a.app, Body: "Desktop notifications are working."}); err != nil {
		return apperror.New(502, err.Error(), err)
	}
	return nil
}

// Mute stops all notifications, for shutdown: turns cancelled by a
// restart should not raise alerts.
func (a *App) Mute() { a.muted.Store(true) }

// send delivers a notification off the caller's goroutine — a turn's
// goroutine never waits on a title lookup or the platform backend. convID
// is the room whose title labels the notification; the lookup happens
// inside. The guards are checked before any work: shutdown-mute, an
// unusable backend, or the owner's switch all skip silently.
func (a *App) send(convID string, n func(room string) dservice.Notification) {
	if a.muted.Load() || !a.sender.Available() || !a.enabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deliveryTimeout)
		defer cancel()
		if a.muted.Load() {
			return
		}
		room := ""
		if convID != "" {
			room = a.title(ctx, convID)
		}
		out := n(room)
		if err := a.sender.Notify(ctx, out); err != nil {
			a.log.Warn("desktop notification failed", "title", out.Title, "error", err)
		}
	}()
}

func (a *App) title(ctx context.Context, convID string) string {
	info, err := a.rooms.Info(ctx, convID)
	if err != nil || strings.TrimSpace(info.Title) == "" {
		return "Untitled thread"
	}
	return oneLine(info.Title, 120)
}

// TurnFinished notifies when a turn started from the web ends, whatever
// opened it — send, edit, or resume. Chat and automation turns report
// through their own paths; a stopped turn is the owner's own doing.
func (a *App) TurnFinished(convID, _ string, external bool, err error) {
	if external {
		return
	}
	if err == nil {
		a.send(convID, func(room string) dservice.Notification {
			return dservice.Notification{Title: "Reply ready", Body: room, Urgency: dservice.UrgencyNormal}
		})
		return
	}
	if apperror.As(err).StatusCode() == 408 {
		return
	}
	a.send(convID, func(room string) dservice.Notification {
		return dservice.Notification{Title: "Turn failed", Body: room + "\n" + oneLine(apperror.As(err).Error(), 200),
			Urgency: dservice.UrgencyNormal}
	})
}

// TurnEvent notifies when an agent asks permission, in any thread — an
// unattended run waits on the answer, so the notification stays up until
// dismissed.
func (a *App) TurnEvent(convID string, ev event.TurnEvent) {
	if ev.Kind() != enum.TurnEventApproval {
		return
	}
	r := ev.Approval()
	what := r.Title()
	if what == "" {
		what = r.Action().String()
	}
	a.send(convID, func(room string) dservice.Notification {
		return dservice.Notification{Title: "Approval needed", Body: oneLine(what, 200) + "\n" + room,
			Urgency: dservice.UrgencyCritical}
	})
}

// AutomationRan notifies when an automation run completes or fails. A
// failing schedule fires often, so failure is normal urgency — it must
// not stack sticky notifications.
func (a *App) AutomationRan(name string, status enum.RunStatus, note string) {
	switch status {
	case enum.RunCompleted:
		a.send("", func(string) dservice.Notification {
			body := name
			if note != "" {
				body += "\n" + oneLine(note, 200)
			}
			return dservice.Notification{Title: "Automation finished", Body: body, Urgency: dservice.UrgencyNormal}
		})
	case enum.RunFailed:
		a.send("", func(string) dservice.Notification {
			return dservice.Notification{Title: "Automation failed", Body: name + "\n" + oneLine(note, 200),
				Urgency: dservice.UrgencyNormal}
		})
	}
}

// UpdateAvailable notifies once per release the update check newly sees.
func (a *App) UpdateAvailable(version string) {
	a.send("", func(string) dservice.Notification {
		return dservice.Notification{Title: "Update available", Body: a.app + " " + version + " is ready to install — open Settings → About",
			Urgency: dservice.UrgencyLow, Icon: "system-software-update"}
	})
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}
