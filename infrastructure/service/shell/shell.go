// Package shell runs the owner's terminals on a pseudo-terminal: their
// login shell in a thread's folder, or a command such as an agent's
// interactive sign-in.
package shell

import (
	"context"
	"os"
	"os/exec"
	"sync"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/system/pty"
)

// Local opens shells on this machine. Env is the environment they start
// with (nil for the server's own); TERM is set for xterm.
type Local struct{ Env []string }

func (l Local) Open(ctx context.Context, sc dservice.ShellCommand, cols, rows int) (dservice.Shell, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	argv := sc.Argv
	if len(argv) == 0 {
		argv = []string{loginShell(), "-l"}
	}
	program, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, err
	}
	dir := sc.Dir
	if dir == "" {
		if dir, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	if cols > 0 && rows > 0 {
		_ = pty.Resize(master, cols, rows)
	}
	cmd := exec.Command(program, argv[1:]...)
	cmd.Dir = dir
	env := l.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string(nil), env...)
	for k, v := range sc.Env {
		env = append(env, k+"="+v)
	}
	// Later entries win, so the terminal's own settings come last.
	cmd.Env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	attach(cmd, slave)
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	// The child holds its own slave; the parent copy must close or master
	// reads never see the end.
	slave.Close()
	return &session{cmd: cmd, master: master}, nil
}

func loginShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		if _, err := exec.LookPath(sh); err == nil {
			return sh
		}
	}
	for _, sh := range []string{"bash", "sh"} {
		if path, err := exec.LookPath(sh); err == nil {
			return path
		}
	}
	return "/bin/sh"
}

type session struct {
	cmd    *exec.Cmd
	master *os.File
	once   sync.Once
}

func (s *session) Read(p []byte) (int, error)  { return s.master.Read(p) }
func (s *session) Write(p []byte) (int, error) { return s.master.Write(p) }
func (s *session) Resize(cols, rows int) error { return pty.Resize(s.master, cols, rows) }
func (s *session) Wait() error                 { return s.cmd.Wait() }

func (s *session) Close() error {
	s.once.Do(func() {
		killTree(s.cmd)
		_ = s.master.Close()
	})
	return nil
}
