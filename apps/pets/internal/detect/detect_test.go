package detect

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// testResolver builds a Resolver with a sandboxed home and a sandboxed PATH.
func testResolver(home string, pathBins map[string]bool) *Resolver {
	env := map[string]string{}
	look := func(name string) (string, error) {
		bin := filepath.Join(home, "bin", name)
		if pathBins[name] {
			return bin, nil
		}
		return "", os.ErrNotExist
	}
	return &Resolver{Home: home, Env: func(k string) string { return env[k] }, LookPath: look, Stat: os.Stat}
}

func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestGoBinaryResolution(t *testing.T) {
	t.Parallel()

	t.Run("env root override", func(t *testing.T) {
		home := t.TempDir()
		r := testResolver(home, nil)
		want := filepath.Join(home, "custom-root", "current", "nusashell")
		writeExec(t, want)
		r.Env = func(k string) string {
			if k == "NUSASHELL_GO_INSTALL_ROOT" {
				return filepath.Join(home, "custom-root")
			}
			return ""
		}
		if got, ok := r.GoBinary(); !ok || got != want {
			t.Fatalf("env root: got %q ok=%v, want %q", got, ok, want)
		}
	})

	t.Run("default install root", func(t *testing.T) {
		home := t.TempDir()
		r := testResolver(home, nil)
		want := filepath.Join(home, ".local/share/nusashell/current/nusashell")
		writeExec(t, want)
		if got, ok := r.GoBinary(); !ok || got != want {
			t.Fatalf("install root: got %q ok=%v, want %q", got, ok, want)
		}
	})

	t.Run("user launcher", func(t *testing.T) {
		home := t.TempDir()
		r := testResolver(home, nil)
		want := filepath.Join(home, ".local/bin/nusashell")
		writeExec(t, want)
		if got, ok := r.GoBinary(); !ok || got != want {
			t.Fatalf("launcher: got %q ok=%v, want %q", got, ok, want)
		}
	})

	t.Run("path lookup", func(t *testing.T) {
		home := t.TempDir()
		r := testResolver(home, map[string]bool{"nusashell": true})
		want := filepath.Join(home, "bin", "nusashell")
		writeExec(t, want)
		if got, ok := r.GoBinary(); !ok || got != want {
			t.Fatalf("lookpath: got %q ok=%v, want %q", got, ok, want)
		}
	})

	t.Run("not installed", func(t *testing.T) {
		r := testResolver(t.TempDir(), nil)
		if _, ok := r.GoBinary(); ok {
			t.Fatal("GoBinary must not resolve when nothing is installed")
		}
	})
}

func TestGoBinarySkipsNonExecutable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, ".local/bin/nusashell")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := testResolver(home, nil)
	if _, ok := r.GoBinary(); ok {
		t.Fatal("non-executable launcher must not count as installed")
	}
}

func TestElectronBinaryResolution(t *testing.T) {
	t.Parallel()

	t.Run("configured wins", func(t *testing.T) {
		home := t.TempDir()
		r := testResolver(home, nil)
		configured := filepath.Join(home, "custom", "nusashell-desktop")
		writeExec(t, configured)
		writeExec(t, filepath.Join(home, ".local/bin/nusashell-desktop"))
		if got, ok := r.ElectronBinary(configured); !ok || got != configured {
			t.Fatalf("configured: got %q ok=%v, want %q", got, ok, configured)
		}
	})

	t.Run("user launcher fallback", func(t *testing.T) {
		home := t.TempDir()
		r := testResolver(home, nil)
		want := filepath.Join(home, ".local/bin/nusashell-desktop")
		writeExec(t, want)
		if got, ok := r.ElectronBinary(""); !ok || got != want {
			t.Fatalf("launcher: got %q ok=%v, want %q", got, ok, want)
		}
	})

	t.Run("env install root", func(t *testing.T) {
		home := t.TempDir()
		root := filepath.Join(home, "electron-root")
		want := filepath.Join(root, "current", "nusashell-desktop")
		writeExec(t, want)
		r := testResolver(home, nil)
		r.Env = func(k string) string {
			if k == "NUSASHELL_ELECTRON_INSTALL_ROOT" {
				return root
			}
			return ""
		}
		if got, ok := r.ElectronBinary(""); !ok || got != want {
			t.Fatalf("electron env root: got %q ok=%v, want %q", got, ok, want)
		}
	})

	t.Run("not installed", func(t *testing.T) {
		r := testResolver(t.TempDir(), nil)
		if _, ok := r.ElectronBinary(""); ok {
			t.Fatal("ElectronBinary must not resolve when nothing is installed")
		}
	})
}

func TestBackendAddrFromWebSocketURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		wsURL string
		want  string
	}{
		{"ws://127.0.0.1:9999/ws", "127.0.0.1:9999"},
		{"wss://host.example:8443/ws", "host.example:8443"},
		{"ws://localhost:1234", "localhost:1234"},
		{"", DefaultBackendAddr},
		{"not a url", DefaultBackendAddr},
	}
	for _, tc := range tests {
		if got := BackendAddr(tc.wsURL); got != tc.want {
			t.Fatalf("BackendAddr(%q) = %q, want %q", tc.wsURL, got, tc.want)
		}
	}
}

func TestGoRunningProbesWebSocketHost(t *testing.T) {
	t.Parallel()
	var gotAddr string
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		gotAddr = address
		return nil, os.ErrNotExist
	}
	if GoRunning("ws://127.0.0.1:9999/ws", dial) {
		t.Fatal("dial error must report not running")
	}
	if gotAddr != "127.0.0.1:9999" {
		t.Fatalf("dialed %q, want 127.0.0.1:9999", gotAddr)
	}
}

func TestGoRunningAcceptsConnection(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	dialer := net.Dialer{}
	if !GoRunning("ws://"+ln.Addr().String()+"/ws", dialer.DialContext) {
		t.Fatal("listening backend must report running")
	}
}

func TestElectronRunningScansProcRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCmdline := func(pid, cmdline string) {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCmdline("42", "/home/u/.local/share/nusashell-electron/current/nusashell-desktop\x00--no-sandbox\x00")
	writeCmdline("120", "./bin/nusashell-pets --assets assets/pets")
	writeCmdline("7", "/home/u/.local/bin/nusashell")
	writeCmdline("9", "/home/u/NusaShell-Electron-0.1.0-linux-x86_64.AppImage")
	if !ElectronRunning(root) {
		t.Fatal("nusashell-desktop process must be detected")
	}

	clean := t.TempDir()
	if ElectronRunning(clean) {
		t.Fatal("empty proc root must report not running")
	}

	// A proc tree without the desktop binary must report not running.
	solo := t.TempDir()
	dir := filepath.Join(solo, "3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/x/nusashell-pets"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ElectronRunning(solo) {
		t.Fatal("pets-only proc tree must report not running")
	}

	if ElectronRunning(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("missing proc root must report not running")
	}
}
