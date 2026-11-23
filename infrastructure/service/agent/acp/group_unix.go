//go:build !windows

package acp

import (
	"os/exec"
	"syscall"
)

// startInOwnGroup puts the agent in its own process group so a signal
// reaches the whole tree, grandchildren included.
func startInOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup signals the agent's process group when it has one, else the
// agent alone.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		_ = syscall.Kill(-cmd.Process.Pid, sig)
		return
	}
	_ = cmd.Process.Signal(sig)
}

// StartInOwnGroup starts cmd in its own process group.
func StartInOwnGroup(cmd *exec.Cmd) { startInOwnGroup(cmd) }

// KillGroup kills cmd's process group.
func KillGroup(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGKILL) }
