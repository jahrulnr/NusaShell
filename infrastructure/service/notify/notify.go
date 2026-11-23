// Package notify shows desktop notifications on the host: notify-send or a
// gdbus call on Linux and BSD, osascript on macOS, a PowerShell toast on
// Windows. Detect picks the backend; a host without one gets a sender
// that reports itself unavailable.
package notify

import (
	"context"
	"errors"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Detect returns the platform backend. appName labels the notifications and
// iconPath is the PNG every one carries, so none shows the system's stock
// icon; empty leaves the icon to the system. macOS shows what osascript is,
// since a notification there takes its icon from the app that sends it.
func Detect(appName, iconPath string) dservice.Notifier { return platformSender(appName, iconPath) }

// none is the sender on hosts without a notification backend.
type none struct{ reason string }

func (n none) Name() string    { return "none" }
func (n none) Available() bool { return false }
func (n none) Reason() string  { return n.reason }
func (n none) Notify(context.Context, dservice.Notification) error {
	return errors.New(n.reason)
}
