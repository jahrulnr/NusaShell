//go:build linux || darwin

package sttinstall

import "golang.org/x/sys/unix"

// diskFree reports free bytes on the volume backing dir (0 = unknown).
// Bsize is int64 on Linux and uint32 on macOS, so it is widened explicitly.
func diskFree(dir string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
