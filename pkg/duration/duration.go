// Package duration parses human-friendly duration strings with support
// for the standard-library time.ParseDuration syntax plus a "d" (day)
// suffix that time.ParseDuration does not accept.
//
// This is a shared leaf package at the module root (under pkg/) so both
// application/ and infrastructure/ can import it without violating Go's
// internal package rule or the Clean Architecture dependency rule. It
// depends only on the standard library.
package duration

import (
	"strings"
	"time"
)

// Parse parses a duration string. It accepts the same syntax as
// time.ParseDuration ("1h30m", "500ms", "2h45m") plus a "d" suffix for
// days ("1d", "2d12h"). An empty string returns a zero duration with no
// error. A "d" suffix is converted to hours before delegating to
// time.ParseDuration, so composite values like "1d12h" work.
func Parse(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		n := strings.TrimSuffix(s, "d")
		hours, err := time.ParseDuration(n + "h")
		if err != nil {
			return 0, err
		}
		return hours * 24, nil
	}
	return time.ParseDuration(s)
}
