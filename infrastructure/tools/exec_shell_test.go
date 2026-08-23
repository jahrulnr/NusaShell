package tools

import (
	"runtime"
	"testing"
)

func TestPickAutoWindowsShell(t *testing.T) {
	if got := pickAutoWindowsShell(true); got != "bash" {
		t.Fatalf("with bash available want bash, got %q", got)
	}
	if got := pickAutoWindowsShell(false); got != "powershell" {
		t.Fatalf("without bash want powershell, got %q", got)
	}
}

func TestShellCommandUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix resolver")
	}
	name, args := shellCommand("", "echo hi")
	if name != "sh" || len(args) != 2 || args[0] != "-c" || args[1] != "echo hi" {
		t.Fatalf("default unix shell wrong: %s %v", name, args)
	}
	name, args = shellCommand("bash", "echo hi")
	if name != "bash" || len(args) != 2 || args[1] != "echo hi" {
		t.Fatalf("bash override wrong: %s %v", name, args)
	}
}

func TestShellCommandWindowsKinds(t *testing.T) {
	// Pure argument-shape checks against the windows resolver are only
	// compiled on windows; here we assert the documented mapping table
	// indirectly via the shared decision function.
	if pickAutoWindowsShell(true) != "bash" || pickAutoWindowsShell(false) != "powershell" {
		t.Fatal("windows auto order must be bash then powershell")
	}
}
