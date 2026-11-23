package config

import "fmt"

// NotificationsConfig controls desktop notifications on the machine the
// server runs on. Desktop is "on" or "off" — a string, since an explicit
// false would read as unset and fall back to the default.
type NotificationsConfig struct {
	Desktop string `json:"desktop" default:"on"`
}

func (c *NotificationsConfig) Normalize() { ApplyDefaults(c) }

func (c NotificationsConfig) validate() error {
	switch c.Desktop {
	case "on", "off":
		return nil
	}
	return fmt.Errorf("notifications.desktop must be on or off, got %q", c.Desktop)
}

// DesktopOn reports whether desktop notifications are wanted.
func (c NotificationsConfig) DesktopOn() bool { return c.Desktop != "off" }
