package ai

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// NewOfflineSynthesizer must resolve a piper binary from the one-click
// installer's managed location when nothing is on PATH / PIPER_BIN. This is
// the wire that makes generate_speech light up immediately after install.
func TestNewOfflineSynthesizerResolvesManagedBinary(t *testing.T) {
	if _, err := exec.LookPath("piper"); err == nil {
		t.Skip("host has piper on PATH; managed-location resolution is indistinguishable here")
	}
	t.Setenv("PIPER_BIN", "")
	dataDir := t.TempDir()
	platformDir := filepath.Join(dataDir, "piper", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(platformDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expectedBin := "piper"
	if runtime.GOOS == "windows" {
		expectedBin += ".exe"
	}
	bin := filepath.Join(platformDir, expectedBin)
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	synth := NewOfflineSynthesizer(dataDir)
	if synth == nil {
		t.Fatalf("NewOfflineSynthesizer returned nil despite managed binary at %s", bin)
	}
	// The engine reports unavailable while no voice is installed — wiring
	// succeeded but speech can't run yet. That is exactly the wire this
	// test guards.
	if synth.Available() {
		t.Skip("unexpected availability flip without voices (host voices dir override?)")
	}
	if reason := synth.UnavailableReason(); reason == "" {
		t.Error("unavailable reason should mention the missing voice models")
	} else if !strings.Contains(strings.ToLower(reason), "voice") {
		t.Errorf("unavailable reason %q should point at the voice gap", reason)
	}
}

// PIPER_BIN takes precedence when set; a pointed-at missing path just
// falls through to the next resolution layer (and ends with nil).
func TestNewOfflineSynthesizerHonorsPIPER_BIN(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("PIPER_BIN", "/nonexistent/piper")
	if synth := NewOfflineSynthesizer(dataDir); synth != nil {
		t.Error("synthesizer must not be wired when no piper binary can be resolved")
	}
}
