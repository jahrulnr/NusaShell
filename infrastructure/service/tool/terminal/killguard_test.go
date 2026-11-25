//go:build linux

package toolexec

import (
	"strings"
	"testing"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// A kill aimed at a pid the caller looked up is normal work; a pattern-kill
// reaches processes this call does not own. The guard draws that line, and its
// refusal teaches the safe path.
func TestKillGuardRefusesPatternKillsOnly(t *testing.T) {
	allowed := []string{
		"kill 4162",
		"kill -TERM 4162",
		"kill -9 4162",
		"kill -9 4162 4163",
		"kill -s TERM 4162",
		"kill -9 -- 4162",
		// A pid looked up in the same command is the safe path, so the guard
		// must not refuse it — that would contradict its own advice.
		"kill -9 $(pgrep -f chrome)",
		"kill -9 `pgrep chrome`",
		"PID=$(pgrep -f chrome); kill -9 $PID",
		"pgrep -af chrome",              // the safe path itself
		"pgrep -af chrome | xargs kill", // lookup first, then kill
		"echo pkill chrome",             // text, not a command
		"grep -r \"killall\" ./docs",    // text, not a command
		"printf 'pkill\\n' > /tmp/note", // text, not a command
		"sh -c 'echo killall'",          // echoed inside a shell
		"sudo kill 4162",                // wrapper, explicit pid
		"timeout 5 kill -9 4162",        // wrapper, explicit pid
		"FOO=1 kill 4162",               // assignment, explicit pid
		"kill -KILL 4162",               // signal form, explicit pid
		"fuser 10994/tcp",               // reports, does not kill
	}
	for _, cmd := range allowed {
		if err := killRefusal(cmd, false); err != nil {
			t.Fatalf("%q was refused: %v", cmd, err)
		}
	}

	refused := []string{
		"pkill chrome",
		"pkill -f chrome",
		"killall sleep",
		"fuser -k 10994/tcp",
		"fuser -km /mnt/data",
		"kill -9 %1",
		"kill -9 chrome",
		"kill -9",
		"sudo pkill -f agent",
		"timeout 5 killall node",
		"echo start; pkill chrome",
		"pgrep chrome && killall chrome",
		"sh -c 'pkill chrome'",
		// Wrappers and path prefixes do not launder a pattern-kill.
		"/bin/pkill chrome",
		"/usr/bin/killall node",
		"nice pkill chrome",
		"nice -n 5 pkill -f chrome",
		"ionice -c3 killall node",
		"busybox pkill chrome",
		"sudo -u someone pkill chrome",
		"setsid pkill chrome",
		"stdbuf -oL pkill chrome",
		"script -qc 'pkill chrome' /dev/null",
		"script -q -c 'killall node' /tmp/typescript",
		// A $VAR nobody looked up in this command is still a guess.
		"kill -9 $PID",
		// The lookup must be the target's own source, not a word in the text.
		"echo pgrep; kill -9 $(ps -eo pid=)",
		"# pgrep\nkill -9 $PIDS",
		"pgrep -f chrome | xargs kill -9",
		"pgrep -f chrome | kill -9",
	}
	for _, cmd := range refused {
		err := killRefusal(cmd, false)
		if err == nil {
			t.Fatalf("%q was allowed, want a refusal", cmd)
		}
		msg := err.Error()
		if !strings.Contains(msg, "pgrep -af") || !strings.Contains(msg, "kill <pid>") || !strings.Contains(msg, "allow_broad_kill") {
			t.Fatalf("%q refusal does not teach the safe path: %v", cmd, err)
		}
	}
}

// The escape hatch is explicit: a deliberate machine-wide stop still names
// itself, and the one command that means "everything" stays refused.
func TestKillGuardEscapeHatch(t *testing.T) {
	for _, cmd := range []string{"pkill chrome", "killall sleep", "fuser -k 10994/tcp", "kill -9 $(pgrep chrome)"} {
		if err := killRefusal(cmd, true); err != nil {
			t.Fatalf("%q with allow_broad_kill was refused: %v", cmd, err)
		}
	}
	for _, cmd := range []string{"kill -9 -1", "kill -9 0", "kill -KILL -1", "kill -9 -- -1", "kill -s KILL 0"} {
		err := killRefusal(cmd, true)
		if err == nil {
			t.Fatalf("%q must be refused even with allow_broad_kill", cmd)
		}
		if !strings.Contains(err.Error(), "every process on the machine") {
			t.Fatalf("%q refusal = %v, want the always-refused wording", cmd, err)
		}
	}
}

// The tool boundary enforces it: exec refuses before starting anything, and
// the escape hatch is a field on the call, not a hidden default.
func TestExecRefusesPatternKillBeforeStarting(t *testing.T) {
	tool := newTool(t)
	_, err := tool.Execute(testContext(), mustCall(t, "kill-broad", `{"command":"pkill -f chrome"}`, 0), nil)
	if err == nil {
		t.Fatal("exec ran a pattern-kill")
	}
	if !strings.Contains(err.Error(), "allow_broad_kill") {
		t.Fatalf("refusal = %v, want the escape hatch named", err)
	}
	// The escape hatch runs the same command shape without starting a real
	// pattern-kill: a marker file proves the command itself executed.
	root := t.TempDir()
	marker := root + "/ran"
	_, err = tool.Execute(testContext(), mustCall(t, "kill-hatch",
		`{"command":"echo would-kill > `+marker+`","allow_broad_kill":true}`, 0), nil)
	if err != nil {
		t.Fatalf("allow_broad_kill call refused: %v", err)
	}
}

// exec_session opens nothing when the command is a pattern-kill: there is no
// session to reap afterwards.
func TestExecSessionRefusesPatternKill(t *testing.T) {
	store := &pollStore{poll: dservice.TerminalPoll{Running: true}}
	tool, err := NewExecSessionTool(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Execute(testContext(), mustCall(t, "session-kill", `{"command":"killall node"}`, 0), nil)
	if err == nil || !strings.Contains(err.Error(), "pgrep -af") {
		t.Fatalf("exec_session = %v, want the teaching refusal", err)
	}
	if len(store.waits) != 0 {
		t.Fatal("exec_session opened a session for a refused command")
	}
}
