//go:build windows

package system

import (
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"
)

// tasksched is the Service over the Windows Task Scheduler — the
// user-level analog of `systemctl --user` (the Service Control Manager
// is system-scope and wants elevation). Registration is a logon task
// owned by the installing user; control verbs are schtasks, and status
// prefers PowerShell because schtasks' query text is localized.
type tasksched struct{}

func (tasksched) Name() string { return "Task Scheduler" }

func schtasks(args ...string) ([]byte, int, error) {
	return execCommand("schtasks", args...)
}

func (tasksched) run(args ...string) error {
	out, code, err := schtasks(args...)
	if err != nil {
		return err
	}
	if code != 0 {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = fmt.Sprintf("exit %d", code)
		}
		return fmt.Errorf("schtasks %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

// powershellPath finds a PowerShell for the status queries — in-box
// powershell.exe first, then a user-installed pwsh.
func powershellPath() (string, bool) {
	if path, err := lookPath("powershell"); err == nil {
		return path, true
	}
	if path, err := lookPath("pwsh"); err == nil {
		return path, true
	}
	return "", false
}

func powershell(script string) ([]byte, int, error) {
	path, ok := powershellPath()
	if !ok {
		return nil, -1, fmt.Errorf("no powershell on PATH")
	}
	// Warning/Progress streams share stdout under CombinedOutput and would
	// poison the one-line protocols the callers parse — silence them.
	return execCommand(path, "-NoProfile", "-NonInteractive", "-Command",
		"$WarningPreference='SilentlyContinue'; $ProgressPreference='SilentlyContinue'; "+script)
}

// psQuote embeds a validated name into a single-quoted PowerShell
// literal — names pass validName, so a single quote can never appear,
// but ” is the escape either way.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Available probes the Schedule service through PowerShell when one is
// installed — Get-Service answers with a locale-free enum name. Without
// PowerShell the floor is that schtasks runs at all; a stopped Schedule
// service still lets the binary answer, so the real failure surfaces at
// whichever verb needs the manager.
func (tasksched) Available() error {
	if _, ok := powershellPath(); ok {
		out, code, err := powershell("(Get-Service Schedule).Status")
		if err == nil && code == 0 {
			if status := strings.TrimSpace(string(out)); status != "Running" {
				return fmt.Errorf("the Task Scheduler service is %s", status)
			}
			return nil
		}
	}
	if _, _, err := schtasks("/Query", "/TN", "nusashell-availability-probe"); err != nil {
		return fmt.Errorf("Task Scheduler is unavailable (%s)", err)
	}
	return nil
}

// taskPrincipal is the installing user in DOMAIN\name form — the shape
// the trigger and principal need on Microsoft-account boxes, where a
// bare USERNAME can differ from the account a logon trigger matches.
func taskPrincipal() (string, error) {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if name == "" {
		name = os.Getenv("USERNAME")
	}
	if name == "" {
		return "", fmt.Errorf("tasksched: the installing user could not be determined")
	}
	if strings.Contains(name, `\`) {
		return name, nil
	}
	domain := os.Getenv("USERDOMAIN")
	if domain == "" {
		domain = os.Getenv("COMPUTERNAME")
	}
	if domain == "" {
		return name, nil
	}
	return domain + `\` + name, nil
}

// writeTaskXML stages the registration document where schtasks can
// read it — the /XML argument takes a file, and schtasks requires it
// UTF-16 encoded.
func writeTaskXML(text string) (string, error) {
	f, err := os.CreateTemp("", "nusashell-task-*.xml")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(taskXMLBytes(text)); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// Register writes or rewrites the logon task, then starts it now —
// ending and re-running the instance when one is already up, so an
// update lands on the new binary.
//
// Spec.Env is intentionally unused: Task Scheduler has no per-task
// environment. It is not needed here — the action runs the installed
// binary, which pins its own CONFIG_PATH from the install location, and
// a logon task inherits the session's user environment (its registry
// PATH), which is what a daemon's children want anyway.
func (s tasksched) Register(spec Spec) error {
	if !validName(spec.Name) {
		return errBadName(spec.Name)
	}
	if spec.Exec == "" {
		return fmt.Errorf("tasksched: Spec.Exec is required")
	}
	if err := s.Available(); err != nil {
		return err
	}
	wasActive := s.Active(spec.Name)
	userID, err := taskPrincipal()
	if err != nil {
		return err
	}
	text, err := renderTaskXML(spec, userID)
	if err != nil {
		return err
	}
	if spec.WorkingDir != "" {
		if err := os.MkdirAll(spec.WorkingDir, 0o755); err != nil {
			return err
		}
	}
	xmlFile, err := writeTaskXML(text)
	if err != nil {
		return err
	}
	defer os.Remove(xmlFile)
	if err := s.run("/Create", "/TN", spec.Name, "/XML", xmlFile, "/F"); err != nil {
		return err
	}
	if wasActive {
		if err := s.run("/End", "/TN", spec.Name); err != nil {
			return err
		}
		// The task is not Running the moment /End returns, but the
		// scheduler still counts the tearing-down instance — an
		// immediate /Run is refused under MultipleInstancesPolicy
		// IgnoreNew. Wait it out.
		s.waitNotRunning(spec.Name)
	}
	return s.run("/Run", "/TN", spec.Name)
}

// waitNotRunning polls until the task leaves Running state — briefly,
// so a wedged instance cannot stall the caller much past the retry
// /Run below.
func (s tasksched) waitNotRunning(name string) {
	for range 50 {
		_, state, err := s.Status(name)
		if err != nil || state != StateRunning {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// taskExists distinguishes "no such task" from "the manager cannot be
// asked". PowerShell answers both definitively; without it a named
// /Query failing is confirmed missing only when a broad /Query still
// gets an answer.
func (s tasksched) taskExists(name string) (bool, error) {
	if _, ok := powershellPath(); ok {
		out, code, err := powershell(
			"if ((Get-Service Schedule).Status -ne 'Running') { 'down' }" +
				" elseif (Get-ScheduledTask -TaskName " + psQuote(name) + " -ErrorAction SilentlyContinue) { 'yes' } else { 'no' }")
		if err != nil {
			return false, err
		}
		if code != 0 {
			return false, fmt.Errorf("task query failed: %s", strings.TrimSpace(string(out)))
		}
		switch strings.TrimSpace(string(out)) {
		case "down":
			return false, fmt.Errorf("the Task Scheduler service is not running")
		case "yes":
			return true, nil
		default:
			return false, nil
		}
	}
	_, code, err := schtasks("/Query", "/TN", name)
	if err != nil {
		return false, err
	}
	if code == 0 {
		return true, nil
	}
	// A named query fails on a missing task and on a dead manager alike.
	// The broad query separates them: an answering manager has tasks to
	// list, a dead one fails again.
	if _, broadCode, broadErr := schtasks("/Query", "/FO", "CSV", "/NH"); broadErr != nil {
		return false, broadErr
	} else if broadCode != 0 {
		return false, fmt.Errorf("Task Scheduler did not answer a query")
	}
	return false, nil
}

// Deregister stops any running instance and deletes the task. A missing
// task is success; an unanswerable manager is an error since nothing
// local can be removed instead. A refused /End is reported in the note —
// the delete may still land, leaving an orphan instance that needs a
// manual kill.
func (s tasksched) Deregister(name string) (string, error) {
	if !validName(name) {
		return "", errBadName(name)
	}
	exists, err := s.taskExists(name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	// /End first: /Delete on a live task can refuse or leave the
	// instance running against a registration that no longer exists.
	note := ""
	if err := s.run("/End", "/TN", name); err != nil && s.Active(name) {
		note = fmt.Sprintf("the running instance could not be stopped: %s", err)
	}
	return note, s.run("/Delete", "/TN", name, "/F")
}

// Start runs the task. Already running is success — the second /Run
// would be refused by IgnoreNew, which reads as a failure for an
// idempotent verb.
func (s tasksched) Start(name string) error {
	if !validName(name) {
		return errBadName(name)
	}
	if s.Active(name) {
		return nil
	}
	return s.run("/Run", "/TN", name)
}

// Stop ends the task's instances. A registered task that is not running
// is already stopped; only a task that stays active after /End fails is
// an error.
func (s tasksched) Stop(name string) error {
	if !validName(name) {
		return errBadName(name)
	}
	if err := s.run("/End", "/TN", name); err != nil {
		if !s.Active(name) {
			return nil
		}
		return err
	}
	return nil
}

// spawnOutsideJob starts commandLine through WMI — deliberately NOT as
// a child of this process. Task Scheduler holds each instance in a job
// object and /End terminates the whole tree, so a helper spawned as our
// child would die at the /End it exists to survive. A WMI-created
// process belongs to the provider host and outlives the teardown.
func spawnOutsideJob(commandLine string) error {
	out, code, err := powershell(
		"$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = " +
			psQuote(commandLine) + " }; if ($r.ReturnValue -ne 0) { exit 4 }")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("WMI process create failed (exit %d): %s", code, strings.TrimSpace(string(out)))
	}
	return nil
}

// Restart queues a stop+start on a detached cmd and returns at once.
// It cannot run inline: a /Run against a live task is refused
// (MultipleInstancesPolicy IgnoreNew), and a caller running inside the
// task is killed by its own /End before a /Run could follow. The ping
// gives the caller's exit a head start; the detached cmd runs outside
// the task's job so it still issues the /Run.
func (s tasksched) Restart(name string) error {
	if !validName(name) {
		return errBadName(name)
	}
	return spawnOutsideJob(fmt.Sprintf(
		`cmd /C ping -n 3 127.0.0.1 >nul & schtasks /End /TN %s & schtasks /Run /TN %s`, name, name))
}

// Status reads the task's runtime state. PowerShell's Get-ScheduledTask
// reports enum names and integer results — locale-independent — so it
// is preferred. The schtasks CSV fallback can only trust the English
// words a default locale prints; anything else degrades to
// StateUnknown instead of a wrong answer.
func (s tasksched) Status(name string) (string, State, error) {
	if !validName(name) {
		return "", StateUnknown, errBadName(name)
	}
	if _, ok := powershellPath(); ok {
		// Exit codes separate "no such task" (3) from "the manager
		// itself cannot be asked" (4) — only the former is a
		// not-registered answer; everything else is a query error.
		out, code, err := powershell(
			"if ((Get-Service Schedule -ErrorAction SilentlyContinue).Status -ne 'Running') { exit 4 };" +
				" try { $t = Get-ScheduledTask -TaskName " + psQuote(name) + " -ErrorAction Stop } catch { exit 3 };" +
				" $i = $t | Get-ScheduledTaskInfo;" +
				` "$($t.State)|$($i.LastTaskResult)|$($i.LastRunTime)|$($i.NextRunTime)"`)
		if err != nil {
			return "", StateUnknown, err
		}
		if code == 3 {
			return fmt.Sprintf("task %s is not registered", name), StateStopped, nil
		}
		if code == 4 {
			return "", StateUnknown, fmt.Errorf("the Task Scheduler service is not running")
		}
		if code != 0 {
			report := strings.TrimSpace(string(out))
			return report, StateUnknown, fmt.Errorf("task status query failed: %s", report)
		}
		info, err := parseTaskInfo(string(out))
		if err != nil {
			return strings.TrimSpace(string(out)), StateUnknown, err
		}
		return info.report(name), info.serviceState(), nil
	}
	out, code, err := schtasks("/Query", "/TN", name, "/FO", "CSV", "/NH")
	report := strings.TrimRight(string(out), "\n")
	if err != nil {
		return report, StateUnknown, err
	}
	if code != 0 {
		if _, broadCode, broadErr := schtasks("/Query", "/FO", "CSV", "/NH"); broadErr == nil && broadCode == 0 {
			return fmt.Sprintf("task %s is not registered", name), StateStopped, nil
		}
		return report, StateUnknown, fmt.Errorf("Task Scheduler did not answer a query")
	}
	status, perr := schedCSVStatus(report)
	if perr != nil {
		return report, StateUnknown, nil
	}
	return report, schedCSVState(status), nil
}

func (s tasksched) Active(name string) bool {
	if !validName(name) {
		return false
	}
	_, state, err := s.Status(name)
	return err == nil && state == StateRunning
}

// ActiveElsewhere distinguishes "the task is up" from "the task is up
// and this process is not it". Task Scheduler has no invocation marker,
// so the check walks the process tree: a task action's parent is the
// Schedule service host (svchost, or taskeng on older Windows), while a
// foreground run has a shell or Explorer parent. A host without
// PowerShell cannot verify the identity and answers false rather than
// claiming the service holds something it might not.
func (s tasksched) ActiveElsewhere(name string) bool {
	if !s.Active(name) {
		return false
	}
	// $PID is this PowerShell child; its parent is the server process. One
	// more hop reaches the server's own parent — the task host for a task,
	// or Explorer/a shell for a foreground run.
	out, code, err := powershell(
		`$p = (Get-CimInstance Win32_Process -Filter "ProcessId=$PID" -ErrorAction SilentlyContinue).ParentProcessId;` +
			` if ($p) { $gp = (Get-CimInstance Win32_Process -Filter "ProcessId=$p" -ErrorAction SilentlyContinue).ParentProcessId;` +
			` if ($gp) { (Get-Process -Id $gp -ErrorAction SilentlyContinue).ProcessName } }`)
	if err != nil || code != 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(string(out))) {
	case "svchost", "taskeng", "services", "taskhostw":
		return false
	}
	return true
}

// PersistAcrossLogout: a logon task lives and dies with the user's
// session — Task Scheduler has no linger equivalent short of a
// system-scope task that wants elevation.
func (tasksched) PersistAcrossLogout() error {
	return fmt.Errorf("%w: %s", ErrUnsupported, "a Task Scheduler logon task cannot outlive the session")
}
