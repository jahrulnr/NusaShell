//go:build windows

package acp

import (
	"os/exec"
	"syscall"
)

// startInOwnGroup is a no-op on Windows: there are no POSIX process groups.
func startInOwnGroup(*exec.Cmd) {}

// signalGroup hard-kills the agent and its children: Windows has no POSIX
// signals, so there is no graceful variant to send. taskkill /T reaches the
// tree; killing only the direct child would let the agent's own children
// outlive, which is the failure this replaces. The direct kill stays as the
// fallback for a host where taskkill cannot be resolved or is refused.
func signalGroup(cmd *exec.Cmd, _ syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := exec.Command("taskkill", taskkillArgs(cmd.Process.Pid)...).Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}

// StartInOwnGroup starts cmd in its own process group.
func StartInOwnGroup(cmd *exec.Cmd) { startInOwnGroup(cmd) }

// KillGroup kills cmd's process tree.
func KillGroup(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGKILL) }
