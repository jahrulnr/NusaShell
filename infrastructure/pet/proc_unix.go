//go:build unix

package pet

import (
	"os/exec"
	"syscall"
)

func applyPetProcAttrs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func petProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

func killPetProcess(cmd *exec.Cmd, force bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killPetPID(cmd.Process.Pid, force)
}

func killPetPID(pid int, force bool) {
	if pid <= 0 {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
	_ = syscall.Kill(pid, sig)
}
