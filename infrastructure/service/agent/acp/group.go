package acp

import "strconv"

// taskkillArgs is the command line that ends a Windows process tree: /T
// includes children and grandchildren, /F skips the terminate prompt.
//
// On Unix an agent runs in its own process group, so a signal reaches its
// whole tree (see group_unix.go). Windows has no process groups and no
// POSIX signals, so killing only the direct child leaves the agent's own
// children running — the agent dies, its tooling does not. It lives in an
// untagged file so the shape stays testable from any host.
func taskkillArgs(pid int) []string {
	return []string{"/T", "/F", "/PID", strconv.Itoa(pid)}
}
