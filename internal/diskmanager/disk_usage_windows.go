//go:build windows
// +build windows

package diskmanager

import (
	"fmt"
	"syscall"
	"unsafe"
)

// GetDiskUsage returns the disk usage percentage for Windows
func GetDiskUsage(baseDir string) (float64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64

	utf16Path, err := syscall.UTF16PtrFromString(baseDir)
	if err != nil {
		return 0, err
	}

	_, _, err = getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(utf16Path)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if err != syscall.Errno(0) {
		return 0, err
	}

	used := totalNumberOfBytes - totalNumberOfFreeBytes

	return (float64(used) / float64(totalNumberOfBytes)) * 100, nil
}

/*
// DiskSpaceInfo holds detailed disk space information.
type DiskSpaceInfo struct {
	TotalBytes uint64
	UsedBytes  uint64
}
*/
// DiskSpaceInfo is defined in the unix counterpart, keep struct definition commented out here
// to avoid redeclaration errors during build, but keep for reference.

// GetDetailedDiskUsage returns the total and used disk space in bytes for the filesystem containing the given path.
func GetDetailedDiskUsage(path string) (DiskSpaceInfo, error) {
	h := syscall.MustLoadDLL("kernel32.dll")
	c := h.MustFindProc("GetDiskFreeSpaceExW")

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64

	ret, _, err := c.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(path))),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)))

	if ret == 0 {
		return DiskSpaceInfo{}, fmt.Errorf("failed to get disk free space for '%s': %w", path, err)
	}

	usedBytes := uint64(totalNumberOfBytes - totalNumberOfFreeBytes)

	return DiskSpaceInfo{
		TotalBytes: uint64(totalNumberOfBytes),
		UsedBytes:  usedBytes,
	}, nil
}
