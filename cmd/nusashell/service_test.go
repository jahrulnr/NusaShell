package main

import (
	"testing"
)

func TestResolveServiceOptionsUsesFlagsAndEnv(t *testing.T) {
	t.Setenv("NUSASHELL_DATA_DIR", "/tmp/nusashell-custom")
	t.Setenv("NUSASHELL_HOST", "127.0.0.1")
	t.Setenv("NUSASHELL_PORT", "8080")
	t.Setenv("NUSASHELL_ALLOW_REMOTE", "1")
	t.Setenv("PATH", "/home/test/.local/bin:/usr/local/bin")

	opts, err := resolveServiceOptions("/opt/nusashell/current/nusashell")
	if err != nil {
		t.Fatalf("resolveServiceOptions: %v", err)
	}
	if opts.BinaryPath != "/opt/nusashell/current/nusashell" {
		t.Fatalf("BinaryPath = %q", opts.BinaryPath)
	}
	if opts.DataDir != "/tmp/nusashell-custom" {
		t.Fatalf("DataDir = %q", opts.DataDir)
	}
	if opts.Host != "127.0.0.1" || opts.Port != "8080" || !opts.AllowRemote {
		t.Fatalf("overrides not propagated: %+v", opts)
	}
	if opts.Path != "/home/test/.local/bin:/usr/local/bin" {
		t.Fatalf("PATH = %q", opts.Path)
	}
}

func TestResolveServiceOptionsDefaults(t *testing.T) {
	t.Setenv("NUSASHELL_DATA_DIR", "")
	t.Setenv("NUSASHELL_HOST", "")
	t.Setenv("NUSASHELL_PORT", "")

	opts, err := resolveServiceOptions("")
	if err != nil {
		t.Fatalf("resolveServiceOptions: %v", err)
	}
	if opts.BinaryPath == "" {
		t.Fatal("BinaryPath must default to the running executable")
	}
	if opts.DataDir == "" {
		t.Fatal("DataDir must default to the OS config dir")
	}
	if opts.Host != "" || opts.Port != "" || opts.AllowRemote {
		t.Fatalf("defaults must stay empty: %+v", opts)
	}
}

func TestServiceCmdRejectsUnknownAction(t *testing.T) {
	if err := serviceCmd([]string{"bogus"}); err == nil {
		t.Fatal("unknown action must fail")
	}
}

func TestServiceCmdRejectsMissingAction(t *testing.T) {
	if err := serviceCmd(nil); err == nil {
		t.Fatal("missing action must fail")
	}
}
