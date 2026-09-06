package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner records every invocation and replays scripted answers.
type fakeRunner struct {
	calls  [][2]string
	script map[string]string // "name arg1 arg2" -> stdout
	err    map[string]error
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, [2]string{name, strings.Join(args, " ")})
	if f.err != nil {
		if err, ok := f.err[key]; ok {
			return "", err
		}
	}
	if f.script != nil {
		return f.script[key], nil
	}
	return "", nil
}

func (f *fakeRunner) has(name string, args ...string) bool {
	key := strings.Join(append([]string{name}, args...), " ")
	for _, call := range f.calls {
		if strings.Join(append([]string{call[0]}, strings.Split(call[1], " ")...), " ") == key {
			return true
		}
	}
	return false
}

func testOptions(t *testing.T) Options {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "current")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "nusashell")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{BinaryPath: bin, DataDir: filepath.Join(t.TempDir(), "nusashell-data")}
}
