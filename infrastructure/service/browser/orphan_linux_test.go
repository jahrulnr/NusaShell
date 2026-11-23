//go:build linux

package browser

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A stale singleton lock — one whose named pid is gone — is cleared
// before the launch, so a crashed previous run cannot stall the next.
func TestReapOrphanClearsAStaleLock(t *testing.T) {
	dir := t.TempDir()
	prof := filepath.Join(dir, "default")
	if err := os.MkdirAll(prof, 0o700); err != nil {
		t.Fatal(err)
	}
	// 4194304 is past the usual pid_max — no live process answers it.
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		target := "host-4194304"
		if name != "SingletonLock" {
			target = "cookie"
		}
		if err := os.Symlink(target, filepath.Join(prof, name)); err != nil {
			t.Fatal(err)
		}
	}
	m := New(Options{ProfileDir: dir})
	m.reapOrphan(prof, "default")
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if _, err := os.Lstat(filepath.Join(prof, name)); err == nil {
			t.Fatalf("%s survived the reap", name)
		}
	}
}

// A lock whose pid is alive but does not run our profile is left alone —
// it may be a user's own browser.
func TestReapOrphanLeavesAForeignProcess(t *testing.T) {
	dir := t.TempDir()
	prof := filepath.Join(dir, "default")
	if err := os.MkdirAll(prof, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("host-"+strconv.Itoa(os.Getpid()), filepath.Join(prof, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	m := New(Options{ProfileDir: dir})
	m.reapOrphan(prof, "default")
	if _, err := os.Lstat(filepath.Join(prof, "SingletonLock")); err != nil {
		t.Fatal("a lock held by a foreign process was removed")
	}
}

// Two sequential Opens share one session and never lock each other out.
func TestSequentialOpensShareTheSession(t *testing.T) {
	if _, _, err := resolveExec("", ""); err != nil {
		t.Skip("no Chrome or Chromium on this host")
	}
	m := New(Options{ProfileDir: t.TempDir()})
	defer m.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s1, err := m.Open(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := m.Open(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatal("the second Open did not return the running session")
	}
	s3, err := m.Open(ctx, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if s3 == s1 {
		t.Fatal("another thread got the same session")
	}
}

// An Open whose context is already dead errors fast and registers no
// session or profile lock the next call would trip over.
func TestOpenHonoursADeadContext(t *testing.T) {
	if _, _, err := resolveExec("", ""); err != nil {
		t.Skip("no Chrome or Chromium on this host")
	}
	dir := t.TempDir()
	m := New(Options{ProfileDir: dir})
	defer m.Shutdown()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Open(ctx, "c1"); err == nil {
		t.Fatal("a dead context still opened a session")
	}
	if s, ok := m.Session("c1"); ok || s != nil {
		t.Fatal("a dead-context open left a session behind")
	}
	// The profile dir must be clean for the next launch — no orphan
	// lock files written by a half-up Chrome.
	entries, _ := os.ReadDir(filepath.Join(dir, "default"))
	for _, e := range entries {
		if e.Name() == "SingletonLock" || e.Name() == "SingletonSocket" {
			t.Fatalf("dead-context open left %s", e.Name())
		}
	}
}
