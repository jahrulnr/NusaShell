//go:build !linux

package shell

import (
	"os"
	"os/exec"
)

// pty.Open fails first off Linux; these keep the package building.
func attach(*exec.Cmd, *os.File) {}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
