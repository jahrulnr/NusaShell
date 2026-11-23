package system

// Task Scheduler registration and status formatting, kept build-tag-free so
// the render and parse paths are exercised by the host test suite. The
// Windows adapter in tasksched.go is the only piece that needs windows.

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// renderTaskXML produces the registration document `schtasks /Create /XML`
// consumes: a logon-triggered task running as the installing user with an
// interactive token — no password is stored anywhere. The settings mirror
// the service contract: IgnoreNew keeps a manual /Run from doubling the
// server, the battery and idle gates are off because a server has no
// reason to care about power or idle state, and failures restart like
// the systemd unit's Restart=on-failure.
func renderTaskXML(spec Spec, userID string) (string, error) {
	for _, field := range []struct{ name, value string }{
		{"Exec", spec.Exec},
		{"WorkingDir", spec.WorkingDir},
		{"Description", spec.Description},
		{"user", userID},
	} {
		if strings.ContainsAny(field.value, "\r\n") {
			return "", fmt.Errorf("tasksched: %s contains a line break", field.name)
		}
	}
	args := make([]string, 0, len(spec.Args))
	for _, arg := range spec.Args {
		if strings.ContainsAny(arg, "\r\n") {
			return "", fmt.Errorf("tasksched: Args contains a line break")
		}
		args = append(args, winArgQuote(arg))
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-16\"?>\r\n")
	b.WriteString("<Task version=\"1.2\" xmlns=\"http://schemas.microsoft.com/windows/2004/02/mit/task\">\r\n")
	b.WriteString("  <RegistrationInfo>\r\n    <Description>" + xmlEscape(spec.Description) + "</Description>\r\n  </RegistrationInfo>\r\n")
	b.WriteString("  <Triggers>\r\n    <LogonTrigger>\r\n      <Enabled>true</Enabled>\r\n      <UserId>" + xmlEscape(userID) + "</UserId>\r\n    </LogonTrigger>\r\n  </Triggers>\r\n")
	b.WriteString("  <Principals>\r\n    <Principal id=\"Author\">\r\n      <UserId>" + xmlEscape(userID) + "</UserId>\r\n      <LogonType>InteractiveToken</LogonType>\r\n      <RunLevel>LeastPrivilege</RunLevel>\r\n    </Principal>\r\n  </Principals>\r\n")
	b.WriteString("  <Settings>\r\n" +
		"    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\r\n" +
		"    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\r\n" +
		"    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\r\n" +
		"    <AllowHardTerminate>true</AllowHardTerminate>\r\n" +
		"    <StartWhenAvailable>true</StartWhenAvailable>\r\n" +
		"    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>\r\n" +
		"    <AllowStartOnDemand>true</AllowStartOnDemand>\r\n" +
		"    <Enabled>true</Enabled>\r\n" +
		"    <Hidden>true</Hidden>\r\n" +
		"    <RunOnlyIfIdle>false</RunOnlyIfIdle>\r\n" +
		"    <WakeToRun>false</WakeToRun>\r\n" +
		"    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\r\n" +
		"    <Priority>7</Priority>\r\n" +
		"    <RestartOnFailure>\r\n      <Interval>PT1M</Interval>\r\n      <Count>3</Count>\r\n    </RestartOnFailure>\r\n" +
		"  </Settings>\r\n")
	b.WriteString("  <Actions Context=\"Author\">\r\n    <Exec>\r\n      <Command>" + xmlEscape(spec.Exec) + "</Command>\r\n")
	if len(args) > 0 {
		b.WriteString("      <Arguments>" + xmlEscape(strings.Join(args, " ")) + "</Arguments>\r\n")
	}
	if spec.WorkingDir != "" {
		b.WriteString("      <WorkingDirectory>" + xmlEscape(spec.WorkingDir) + "</WorkingDirectory>\r\n")
	}
	b.WriteString("    </Exec>\r\n  </Actions>\r\n</Task>\r\n")
	return b.String(), nil
}

// taskXMLBytes encodes the registration document the way schtasks
// /Create /XML demands it: UTF-16LE with a byte-order mark, matching
// the encoding="UTF-16" declaration.
func taskXMLBytes(text string) []byte {
	encoded := utf16.Encode([]rune(text))
	out := make([]byte, 2+2*len(encoded))
	out[0], out[1] = 0xFF, 0xFE
	for i, u := range encoded {
		binary.LittleEndian.PutUint16(out[2+2*i:], u)
	}
	return out
}

// winArgQuote renders one argument for the Task Scheduler <Arguments>
// element, whose content is concatenated onto the CreateProcess command
// line. Quoting follows CommandLineToArgvW: a value needs quotes only
// when it is empty or carries whitespace or a quote, embedded quotes
// are backslash-escaped, and backslashes double up only where they
// precede a quote or close the string.
func winArgQuote(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			backslashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
		default:
			b.WriteString(strings.Repeat(`\`, backslashes))
			b.WriteRune(r)
		}
		backslashes = 0
	}
	b.WriteString(strings.Repeat(`\`, backslashes*2))
	b.WriteByte('"')
	return b.String()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// taskInfo is the runtime state PowerShell reports for a registered
// task, rendered by tasksched.go as one pipe-separated line.
type taskInfo struct {
	State      string
	LastResult int64
	LastRun    string
	NextRun    string
}

// Task Scheduler LastTaskResult values — the SCHED_S_* "the task is
// fine" answers. Anything outside this set and zero is a real failure
// code from the last run.
const (
	taskResultReady      = 0x41300
	taskResultRunning    = 0x41301
	taskResultDisabled   = 0x41302
	taskResultNeverRan   = 0x41303
	taskResultNoMoreRuns = 0x41304
	taskResultNotSched   = 0x41305
	taskResultTerminated = 0x41306
	taskResultNoTriggers = 0x41307
)

func benignTaskResult(result int64) bool {
	switch result {
	case 0, taskResultReady, taskResultDisabled, taskResultNeverRan,
		taskResultNoMoreRuns, taskResultNotSched, taskResultTerminated,
		taskResultNoTriggers:
		return true
	default:
		return false
	}
}

// parseTaskInfo reads the "State|LastTaskResult|LastRunTime|NextRunTime"
// line the adapter's PowerShell emits — locale-independent because State
// is an enum name and the result is an integer.
func parseTaskInfo(out string) (taskInfo, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return taskInfo{}, fmt.Errorf("tasksched: unparseable status %q", line)
	}
	result, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return taskInfo{}, fmt.Errorf("tasksched: unparseable last result in %q", line)
	}
	info := taskInfo{State: strings.TrimSpace(parts[0]), LastResult: result}
	if len(parts) > 2 {
		info.LastRun = strings.TrimSpace(parts[2])
	}
	if len(parts) > 3 {
		info.NextRun = strings.TrimSpace(parts[3])
	}
	return info, nil
}

// serviceState maps the task's runtime fields onto the portable State.
// Get-ScheduledTask State is an enum, so the names are stable; a
// "Ready" task whose last run crashed reports the failure through
// LastResult and maps to StateFailed.
func (t taskInfo) serviceState() State {
	switch t.State {
	case "Running":
		return StateRunning
	case "Ready", "Queued", "Disabled":
		if t.LastResult == taskResultRunning {
			return StateRunning
		}
		if benignTaskResult(t.LastResult) {
			return StateStopped
		}
		return StateFailed
	default:
		return StateUnknown
	}
}

func (t taskInfo) report(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task Scheduler task %s\n", name)
	fmt.Fprintf(&b, "  State:       %s\n", t.State)
	fmt.Fprintf(&b, "  Last result: %d\n", t.LastResult)
	if t.LastRun != "" {
		fmt.Fprintf(&b, "  Last run:    %s\n", t.LastRun)
	}
	if t.NextRun != "" {
		fmt.Fprintf(&b, "  Next run:    %s", t.NextRun)
	}
	return strings.TrimRight(b.String(), "\n")
}

// schedCSVStatus reads the third column of `schtasks /Query /TN name
// /FO CSV /NH` — "TaskName","Next Run Time","Status". The Status text
// is localized; the caller decides how much of it it can trust.
func schedCSVStatus(out string) (string, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	rec, err := csv.NewReader(strings.NewReader(line)).Read()
	if err != nil {
		return "", err
	}
	if len(rec) < 3 {
		return "", fmt.Errorf("tasksched: unparseable query row %q", line)
	}
	return rec[2], nil
}

// schedCSVState maps the English words a default-locale schtasks prints
// onto State. Any other text — another locale's word for "Ready" — is
// honestly StateUnknown rather than a lucky guess.
func schedCSVState(status string) State {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return StateRunning
	case "ready", "queued", "disabled":
		return StateStopped
	case "could not start":
		return StateFailed
	default:
		return StateUnknown
	}
}
