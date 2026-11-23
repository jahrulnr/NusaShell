package service

import "context"

// Urgency ranks a desktop notification. Backends without urgency ignore it.
type Urgency string

const (
	UrgencyLow      Urgency = "low"
	UrgencyNormal   Urgency = "normal"
	UrgencyCritical Urgency = "critical"
)

// Notification is one desktop notification. Icon optionally overrides the
// backend's app icon with a platform icon name or an absolute image path;
// backends that cannot show one ignore it.
type Notification struct {
	Title   string
	Body    string
	Urgency Urgency
	Icon    string
}

// Notifier shows notifications on the desktop of the machine NusaShell
// runs on.
type Notifier interface {
	// Name identifies the backend: "notify-send", "gdbus", "osascript",
	// "powershell", or "none".
	Name() string
	// Available reports whether a notification can be shown now — false
	// on a headless host.
	Available() bool
	// Reason says why Available is false.
	Reason() string
	Notify(ctx context.Context, n Notification) error
}
