//go:build linux

package browser

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// reapOrphan removes a leftover Chrome of a previous run from the
// profile before a fresh launch, so the process-singleton lock can't
// stall it. The lock is a symlink "hostname-pid": a pid that no longer
// exists leaves a stale lock Chrome refuses on; a pid still running
// under THIS profile dir is our own orphan and is killed. A live
// process under any other profile — a user's own browser — is never
// touched.
func (m *Manager) reapOrphan(dir, profile string) {
	link, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return // no lock file: nothing to reap
	}
	pid, err := strconv.Atoi(link[strings.LastIndex(link, "-")+1:])
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	alive := func() bool {
		err := proc.Signal(syscall.Signal(0))
		// A gone pid reports ESRCH through a raw kill, or
		// ErrProcessDone through pidfd tracking — both mean dead.
		// EPERM keeps the process alive and foreign.
		return err == nil || (!errors.Is(err, syscall.ESRCH) && !errors.Is(err, os.ErrProcessDone))
	}
	if !alive() {
		m.log.Info("removing a stale browser profile lock", "profile", profile, "lock", link)
		removeProfileLocks(dir)
		return
	}
	// Alive — ours only when it was launched with this profile dir.
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || !strings.Contains(string(cmdline), "user-data-dir="+dir) {
		return
	}
	m.log.Warn("killing an orphaned browser from a previous run", "profile", profile, "pid", pid)
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && alive() {
		time.Sleep(100 * time.Millisecond)
	}
	if alive() {
		_ = proc.Kill()
	}
	removeProfileLocks(dir)
}

// removeProfileLocks clears Chrome's singleton artifacts once their
// holder is gone — the lock itself plus the socket and cookie it
// names, which a fresh launch would collide with the same way.
func removeProfileLocks(dir string) {
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
