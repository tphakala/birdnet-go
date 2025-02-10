//go:build windows

package datastore

import (
	"golang.org/x/sys/windows"
)

func getDiskFreeSpace(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var free, total, totalFree uint64
	err = windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree)
	if err != nil {
		return 0, err
	}

	return free, nil
}
