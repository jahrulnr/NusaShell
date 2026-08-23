//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// applyPlatformAttrs puts the child in its own process group so the whole
// tree can be killed with one signal.
func applyPlatformAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree terminates the child and everything it spawned. SIGKILL is
// used directly: this path only runs on cancellation or timeout, where a
// graceful shutdown has already failed by definition.
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// shellCommand resolves the requested shell kind into an executable plus
// argument prefix. On Unix the default is POSIX sh; "bash" is honored when
// installed. Other kinds fall back to sh.
func shellCommand(kind, command string) (string, []string) {
	switch kind {
	case "bash":
		if _, err := exec.LookPath("bash"); err == nil {
			return "bash", []string{"-c", command}
		}
	}
	return "sh", []string{"-c", command}
}
