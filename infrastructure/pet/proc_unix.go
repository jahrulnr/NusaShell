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

// waitPetIfExited reaps the child with WNOHANG. kill(pid,0) stays true for
// zombies, so a settle check must Wait4 to detect an immediate exit.
func waitPetIfExited(cmd *exec.Cmd) (exited bool, waitErr error) {
	if cmd == nil || cmd.Process == nil {
		return true, nil
	}
	var status syscall.WaitStatus
	wpid, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil)
	if err != nil || wpid == 0 {
		return false, nil
	}
	return true, nil
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
