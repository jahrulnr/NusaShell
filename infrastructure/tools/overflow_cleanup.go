package tools

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// ToolOverflowMaxAge is how long spill files stay in the platform temp
// dir before the sweeper removes them. OS reboot may empty /tmp earlier.
const ToolOverflowMaxAge = 24 * time.Hour

const toolOverflowSweepInterval = time.Hour

// SweepToolOverflow deletes regular files under the overflow directory
// whose mtime is at least maxAge before now. Missing dir is a no-op.
// Subdirectories are left untouched.
func SweepToolOverflow(now time.Time, maxAge time.Duration) (int, error) {
	dir := filepath.Join(os.TempDir(), toolOverflowDirName)
	return sweepToolOverflowDir(dir, now, maxAge)
}

func sweepToolOverflowDir(dir string, now time.Time, maxAge time.Duration) (int, error) {
	if maxAge <= 0 {
		maxAge = ToolOverflowMaxAge
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if now.Sub(info.ModTime()) < maxAge {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			continue
		}
		removed++
	}
	return removed, nil
}

// RunOverflowCleanup sweeps immediately, then every hour, until ctx is
// cancelled. Intended as a single goSafe background loop.
func RunOverflowCleanup(ctx context.Context) {
	runOverflowCleanup(ctx, ToolOverflowMaxAge, toolOverflowSweepInterval, time.Now)
}

func runOverflowCleanup(ctx context.Context, maxAge, interval time.Duration, now func() time.Time) {
	if interval <= 0 {
		interval = toolOverflowSweepInterval
	}
	sweep := func() {
		_, _ = SweepToolOverflow(now(), maxAge)
	}
	sweep()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
