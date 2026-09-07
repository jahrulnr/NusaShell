//go:build !unix

package pet

import "os/exec"

func applyPetProcAttrs(cmd *exec.Cmd) {}

func petProcessAlive(pid int) bool {
	return false
}

func killPetProcess(cmd *exec.Cmd, force bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = force
}

func killPetPID(pid int, force bool) {
	_ = pid
	_ = force
}

func waitPetIfExited(cmd *exec.Cmd) (bool, error) {
	_ = cmd
	return false, nil
}
