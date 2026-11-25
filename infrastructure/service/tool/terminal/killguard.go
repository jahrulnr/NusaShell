package toolexec

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// The broad-kill guard: a command that stops "everything that matches" is one
// keystroke away from killing another thread's work, an agent's process, or
// this server itself. Eleven such calls on the owner's machine already did.
// The rule is narrow on purpose — a kill aimed at a pid the model actually
// looked up is normal work; a pattern-kill is not.
//
// This closes the shortcuts people reach for without weighing them, not every
// possible way to kill a process. The wrapper list is closed by decision:
// `sudo`, `doas`, `env`, `command`, `exec`, `builtin`, `nohup`, `time`,
// `timeout`, `xargs`, `nice`, `ionice`, `busybox`, `setsid`, `stdbuf`, and a
// `script -c` value are covered because they are the ordinary way a command is
// wrapped; a caller who writes a script, base64-decodes one, or builds a
// command with a tool this list does not know is out of scope — chasing that
// is endless, and a guard that pretends to be a sandbox is worse than one that
// says what it is.

// killRefusal explains a refused command, or returns nil when the command may
// run. allowBroad is the caller's escape hatch: a deliberate machine-wide
// stop, never a convenience.
func killRefusal(command string, allowBroad bool) error {
	// A pid the target itself looks up — `kill -9 $(pgrep -f x)` or
	// `PID=$(pgrep -f x); kill -9 $PID` — is the safe path this guard
	// teaches, so it must not refuse it. The lookup has to be the target's own
	// source: a `pgrep` word elsewhere in the text (an echo, a comment, a
	// different substitution) proves nothing.
	return killRefusalDepth(command, allowBroad, pgrepSources(command), 0)
}

// scriptCommand returns the command `script -c` would run. The value follows
// the flag, either as `-c CMD` or inside a combined short flag (`-qc CMD`).
func scriptCommand(args []string) (string, bool) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		if strings.Contains(a[1:], "c") && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// pgrepSources maps a shell variable to whether it was filled from a pgrep
// substitution in this same command — `PID=$(pgrep -f x)`.
func pgrepSources(command string) map[string]bool {
	out := map[string]bool{}
	for _, segment := range shellSegments(command) {
		words := shellWords(segment)
		for _, w := range words {
			name, value, ok := strings.Cut(w, "=")
			if !ok || !isAssignment(name+"=") {
				continue
			}
			if isPgrepLookup(value) {
				out[name] = true
			}
		}
	}
	return out
}

// isPgrepLookup reports a substitution that runs exactly one pgrep — the pid
// this command just looked up, not a stream piped through xargs.
func isPgrepLookup(value string) bool {
	value = strings.Trim(value, "'\"")
	var inner string
	switch {
	case strings.HasPrefix(value, "$(") && strings.HasSuffix(value, ")"):
		inner = value[2 : len(value)-1]
	case strings.HasPrefix(value, "`") && strings.HasSuffix(value, "`"):
		inner = value[1 : len(value)-1]
	default:
		return false
	}
	if strings.ContainsAny(inner, "|;&\n") {
		return false // a pipeline of pids is not one looked-up pid
	}
	name, _ := commandWord(inner)
	return filepath.Base(name) == "pgrep"
}

// killRefusalDepth follows the places a command can hide another one: a shell
// running a script (`sh -c`), `eval`, and command substitutions. Without that,
// `sh -c 'pkill chrome'` or `echo $(pkill chrome)` would walk straight past
// the guard.
func killRefusalDepth(command string, allowBroad bool, pgrepVars map[string]bool, depth int) error {
	if depth > 4 {
		return nil
	}
	for _, segment := range shellSegments(command) {
		name, args := commandWord(segment)
		if isShell(name) && hasScriptFlag(args) {
			if script, ok := scriptArg(args); ok {
				if err := killRefusalDepth(unquote(script), allowBroad, pgrepVars, depth+1); err != nil {
					return err
				}
			}
		}
		if name == "script" {
			// `script -qc 'pkill x' /dev/null` runs its -c value; the file
			// argument is only where the transcript goes.
			if cmd, ok := scriptCommand(args); ok {
				if err := killRefusalDepth(unquote(cmd), allowBroad, pgrepVars, depth+1); err != nil {
					return err
				}
			}
			continue
		}
		if name == "eval" {
			if err := killRefusalDepth(unquote(strings.Join(args, " ")), allowBroad, pgrepVars, depth+1); err != nil {
				return err
			}
		}
		for _, sub := range substitutions(segment) {
			if err := killRefusalDepth(sub, allowBroad, pgrepVars, depth+1); err != nil {
				return err
			}
		}
		switch name {
		case "pkill", "killall":
			if allowBroad {
				continue
			}
			return killError(name, false)
		case "fuser":
			if hasKillFlag(args) && !allowBroad {
				return killError("fuser -k", false)
			}
		case "kill":
			if !hasKillSignal(args) {
				continue // a plain TERM to whatever the caller named
			}
			explicit, always := killTargets(args)
			switch {
			case always:
				return killError("kill -9 -1", true)
			case explicit:
				continue // pids written out
			case lookedUpTargets(args, pgrepVars):
				continue // the target itself is a pgrep result
			case allowBroad:
				continue
			default:
				return killError("kill -9", false)
			}
		}
	}
	return nil
}

// lookedUpTargets reports whether every target is the one pid this command
// just looked up: a bare `$(pgrep …)`/backtick substitution, or a variable a
// pgrep assignment filled. Anything else — no target at all, a pipeline, a
// variable from elsewhere — is not a lookup.
func lookedUpTargets(args []string, pgrepVars map[string]bool) bool {
	var targets []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-s" || a == "-n" || a == "--signal":
			i++
			continue
		case strings.HasPrefix(a, "-") && a != "-1":
			continue
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) != 1 {
		return false
	}
	target := strings.Trim(targets[0], "'\"")
	if isPgrepLookup(target) {
		return true
	}
	name := strings.TrimPrefix(strings.TrimPrefix(target, "${"), "$")
	name = strings.TrimSuffix(name, "}")
	return pgrepVars[name] && name != ""
}

// killError is the refusal text: it teaches the safe path instead of only
// saying no, because the model has to do something next.
func killError(tool string, always bool) error {
	if always {
		return fmt.Errorf("`%s` signals every process on the machine, this server included — it is refused even with allow_broad_kill. Name the process instead: `pgrep -af <pattern>`, then `kill <pid>`", tool)
	}
	return fmt.Errorf("`%s` stops every process that matches, this server and other threads included. Find the one you meant first: `pgrep -af <pattern>`, then `kill <pid>`. If a machine-wide stop is really intended, rerun with allow_broad_kill: true", tool)
}

// shellSegments splits a command line at its separators, respecting quotes and
// escapes, so `echo "pkill"` is not read as a pkill. Each piece is one command
// position that may hold a kill.
func shellSegments(command string) []string {
	var (
		out   []string
		start int
		quote rune
		prev  rune
	)
	flush := func(end int) {
		if seg := strings.TrimSpace(command[start:end]); seg != "" {
			out = append(out, seg)
		}
	}
	for i, r := range command {
		switch {
		case quote != 0:
			if r == quote && prev != '\\' {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ';' || r == '\n' || r == '|' || r == '&':
			flush(i)
			start = i + 1
		}
		prev = r
	}
	flush(len(command))
	return out
}

// shellWords splits one command into words, keeping a quoted run, a
// substitution, and a backtick expression whole: `kill -9 $(pgrep -f x)` has
// two words, not four, so the target can be judged as the one expression it is.
func shellWords(segment string) []string {
	var (
		out   []string
		cur   strings.Builder
		depth int
		quote rune
		tick  bool
		prev  rune
	)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range segment {
		switch {
		case tick:
			cur.WriteRune(r)
			if r == '`' && prev != '\\' {
				tick = false
			}
		case quote != 0:
			cur.WriteRune(r)
			if r == quote && prev != '\\' {
				quote = 0
			}
		case depth > 0:
			cur.WriteRune(r)
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			}
		case r == '(' && strings.HasSuffix(cur.String(), "$"):
			cur.WriteRune(r)
			depth = 1
		case r == '`':
			cur.WriteRune(r)
			tick = true
		case r == '\'' || r == '"':
			cur.WriteRune(r)
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
		}
		prev = r
	}
	flush()
	return out
}

// commandWord returns the command a segment runs, after the wrappers that do
// not change what it is: assignments, sudo, env, nohup, timeout, xargs.
func commandWord(segment string) (string, []string) {
	words := shellWords(segment)
	for i := 0; i < len(words); i++ {
		w := strings.Trim(words[i], "'\"")
		w = filepath.Base(w) // /bin/pkill is pkill
		switch {
		case isAssignment(w):
			continue
		case w == "sudo" || w == "doas":
			// Their flags carry values: `sudo -u someone pkill …`.
			for i+1 < len(words) && strings.HasPrefix(words[i+1], "-") {
				i++
				if sudoFlagTakesValue(words[i]) && i+1 < len(words) && !strings.HasPrefix(words[i+1], "-") {
					i++
				}
			}
			continue
		case w == "command" || w == "exec" || w == "builtin" || w == "nohup" || w == "time":
			continue
		case w == "setsid" || w == "stdbuf":
			// A wrapper and its flags: `setsid pkill x`, `stdbuf -oL pkill x`.
			for i+1 < len(words) && strings.HasPrefix(words[i+1], "-") {
				i++
			}
			continue
		case w == "nice" || w == "ionice" || w == "busybox":
			// Their own flags and a numeric value sit between them and the
			// command: `nice -n 5 pkill`, `ionice -c3 killall`.
			for i+1 < len(words) && (strings.HasPrefix(words[i+1], "-") || isNumeric(words[i+1])) {
				i++
			}
			continue
		case w == "env":
			continue
		case w == "xargs":
			continue
		case w == "timeout":
			// timeout [flags] DURATION command …
			if i+1 < len(words) && !strings.HasPrefix(words[i+1], "-") {
				i++
			}
			continue
		case strings.HasPrefix(w, "-") && i > 0 && isWrapper(words[i-1]):
			continue // a wrapper's own flag
		default:
			return w, words[i+1:]
		}
	}
	return "", nil
}

func isWrapper(w string) bool {
	switch filepath.Base(strings.Trim(w, "'\"")) {
	case "sudo", "doas", "env", "xargs", "timeout", "time", "nice", "ionice", "busybox", "setsid", "stdbuf":
		return true
	}
	return false
}

// sudoFlagTakesValue reports the sudo/doas flags whose argument is a separate
// word — the user, group, prompt, or host list a kill could hide behind.
func sudoFlagTakesValue(flag string) bool {
	switch strings.TrimPrefix(flag, "--") {
	case "-u", "--user", "-g", "--group", "-p", "--prompt", "-C", "--close-from",
		"-h", "--host", "-r", "--role", "-t", "--type", "-U", "--other-user":
		return true
	}
	return false
}

// isNumeric reports a bare number — a wrapper's value, not a command.
func isNumeric(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// isAssignment is a leading VAR=value, which runs nothing by itself.
func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for _, r := range w[:i] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// hasKillFlag reports fuser's kill switch: -k, -km, -mkill, --kill.
func hasKillFlag(args []string) bool {
	for _, a := range args {
		if a == "--kill" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "k")) {
			return true
		}
	}
	return false
}

// hasKillSignal reports whether a kill invocation asks for KILL rather than a
// polite signal: -9, -KILL, -SIGKILL, -s KILL, -s 9, -n 9.
func hasKillSignal(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-9" || a == "--signal=9" || a == "--signal=KILL" || a == "--signal=SIGKILL":
			return true
		case a == "-KILL" || a == "-SIGKILL":
			return true
		case (a == "-s" || a == "-n" || a == "--signal") && i+1 < len(args):
			switch strings.ToUpper(args[i+1]) {
			case "9", "KILL", "SIGKILL":
				return true
			}
		}
	}
	return false
}

// killTargets splits the operands of a kill and reports whether they are all
// explicit positive pids. always marks -1 and 0, which mean "everything" and
// "this process group" — those stay refused whatever the caller allows.
func killTargets(args []string) (explicit, always bool) {
	var targets []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-s" || a == "-n" || a == "--signal":
			i++ // its value is a signal, not a target
			continue
		case a == "--":
			targets = append(targets, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-") && a != "-1":
			continue // a flag, or a signal form like -KILL
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		return false, false
	}
	allExplicit := true
	for _, t := range targets {
		if t == "-1" || t == "0" {
			return false, true
		}
		for _, r := range t {
			if !unicode.IsDigit(r) {
				allExplicit = false
				break
			}
		}
		if t == "" {
			allExplicit = false
		}
	}
	return allExplicit, false
}

// unquote strips one layer of shell quoting: a script handed to `sh -c` is
// written as one quoted word, and its commands must be read, not the quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// isShell reports a command that can run a script handed to it.
func isShell(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "ash", "fish":
		return true
	}
	return false
}

// hasScriptFlag reports the -c form (also -ec, -lc, …).
func hasScriptFlag(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "c") {
			return true
		}
	}
	return false
}

// scriptArg returns the script a shell -c carries: the first operand after the
// flags.
func scriptArg(args []string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			continue
		}
		return a, true
	}
	return "", false
}

// substitutions returns the bodies of $( … ) and `…` in a segment — each is a
// command that runs even when the segment's own word is harmless.
func substitutions(segment string) []string {
	var out []string
	for i := 0; i < len(segment); i++ {
		switch {
		case segment[i] == '$' && i+1 < len(segment) && segment[i+1] == '(':
			depth, j := 1, i+2
			for ; j < len(segment) && depth > 0; j++ {
				switch segment[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth == 0 {
				out = append(out, segment[i+2:j-1])
				i = j - 1
			}
		case segment[i] == '`':
			if end := strings.IndexByte(segment[i+1:], '`'); end >= 0 {
				out = append(out, segment[i+1:i+1+end])
				i += end + 1
			}
		}
	}
	return out
}
