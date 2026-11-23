//go:build linux

package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// attach makes the slave the shell's controlling terminal in a session of
// its own, so job control works and the whole tree can be killed.
func attach(cmd *exec.Cmd, slave *os.File) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
