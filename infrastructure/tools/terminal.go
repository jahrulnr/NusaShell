package tools

import (
	"fmt"
	"os"
	"strconv"
	"time"

	clock "nusashell/pkg/time"
)

// Terminal-style rendering helpers for tool output bodies. The header stays
// YAML front matter (see yamlmd.go); the body mimics familiar CLI output
// (ls -l) which LLMs parse with near-perfect comprehension and which costs
// fewer tokens than repeating JSON keys on every row.

// humanSize formats a byte count the way ls -h does: raw bytes below 1K,
// then K/M/G/... with one decimal while the value is under 10.
func humanSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10)
	}
	const units = "KMGTPE"
	f := float64(n)
	i := -1
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if f < 10 {
		return fmt.Sprintf("%.1f%c", f, units[i])
	}
	return fmt.Sprintf("%.0f%c", f, units[i])
}

// lsTime formats a modification time ls-style: "Jan 02 15:04" for recent
// files, "Jan 02  2006" once the file is older than ~6 months (or in the
// future).
func lsTime(t, now time.Time) string {
	const recent = 6 * 30 * 24 * time.Hour // ~6 months
	if now.Sub(t) > recent || t.After(now) {
		return clock.NewTime(t).Format("Jan 02  2006")
	}
	return clock.NewTime(t).Format("Jan 02 15:04")
}

// lsLine renders one directory entry as an ls -l style line:
// mode, size, mtime, name. Owner/group are intentionally dropped — agents
// rarely need them and they cost tokens.
func lsLine(info os.FileInfo, now time.Time) string {
	return fmt.Sprintf("%s %8s %s %s",
		info.Mode().String(),
		humanSize(info.Size()),
		lsTime(info.ModTime(), now),
		info.Name(),
	)
}
