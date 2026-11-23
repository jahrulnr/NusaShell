//go:build linux

package toolexec

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

const interruptSignal = syscall.SIGINT

// sessionCommand is shellCommand minus the ctx binding — a session
// outlives its opening call, so exec.CommandContext is not used. Setpgid
// keeps the kill discipline identical: the child leads its own group and
// killGroup reaches the whole tree.
func sessionCommand(command string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// ptyCommand binds the slave side to 0/1/2 in a fresh session whose
// controlling terminal is that slave — Setsid makes the child a session
// leader (and process-group leader, so killGroup still reaches the
// tree), Setctty/Ctty:0 attaches stdin as the ctty.
func ptyCommand(command string, slave *os.File) (*exec.Cmd, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	return cmd, nil
}

// killGroup signals the whole process group — -pid targets the group the
// child leads. ESRCH means the tree is already gone, which is success.
func killGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (s *terminalSession) terminateProcess() {
	_ = killGroup(s.cmd.Process.Pid, syscall.SIGKILL)
}
