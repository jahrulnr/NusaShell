package system

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalTimezone names this machine's IANA time zone: $TZ when valid, then
// Go's local zone, then the /etc/localtime link. It answers "UTC" when
// none names a loadable zone.
func LocalTimezone() string {
	if zone := strings.TrimPrefix(os.Getenv("TZ"), ":"); zone != "" {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	if name := time.Local.String(); name != "Local" && name != "" {
		if _, err := time.LoadLocation(name); err == nil {
			return name
		}
	}
	if path, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(path, "/zoneinfo/"); ok {
			if _, err := time.LoadLocation(zone); err == nil {
				return zone
			}
		}
	}
	return "UTC"
}
