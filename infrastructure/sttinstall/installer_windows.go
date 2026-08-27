//go:build windows

package sttinstall

import "golang.org/x/sys/windows"

// diskFree reports free bytes on the volume backing dir (0 = unknown).
func diskFree(dir string) int64 {
	ptr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0
	}
	var freeBytesAvailableToCaller uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeBytesAvailableToCaller, nil, nil); err != nil {
		return 0
	}
	return int64(freeBytesAvailableToCaller)
}
