//go:build linux

package system

// candidates is the preference order on systems where several managers
// coexist — native init wins over user-installed supervisors. Later
// managers (supervisord, openrc --user, dinit, runit, cron) slot in
// here as their files land; each stays a build-tag-free file because
// supervisord and cron are not linux-only in principle.
var candidates = []candidate{
	{"systemd", systemd{}, binaryOnPath("systemctl")},
}
