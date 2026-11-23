//go:build windows

package system

// candidates is the preference order on Windows — Task Scheduler is
// the user-level analog of systemd --user (the Service Control
// Manager is system-scope and wants elevation).
var candidates = []candidate{
	{"tasksched", tasksched{}, binaryOnPath("schtasks")},
}
