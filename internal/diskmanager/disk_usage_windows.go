//go:build windows
// +build windows

package diskmanager

import (
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

// GetAvailableSpace returns the available disk space in bytes
func GetAvailableSpace(baseDir string) (uint64, error) {
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

	return uint64(freeBytesAvailable), nil
}
