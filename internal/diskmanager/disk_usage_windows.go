//go:build windows
// +build windows

package diskmanager

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// GetDiskUsage returns the disk usage percentage for Windows
func GetDiskUsage(baseDir string) (float64, error) {
	startTime := time.Now()
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64

	utf16Path, err := syscall.UTF16PtrFromString(baseDir)
	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to convert path to UTF16: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryDiskUsage).
			Context("path", baseDir).
			Context("operation", "utf16_conversion").
			Timing("disk_usage_check", time.Since(startTime)).
			Build()
		return 0, descriptiveErr
	}

	_, _, err = getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(utf16Path)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if err != syscall.Errno(0) {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to get Windows disk free space: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryDiskUsage).
			Context("path", baseDir).
			Context("operation", "get_disk_free_space").
			Timing("disk_usage_check", time.Since(startTime)).
			Build()
		return 0, descriptiveErr
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
	startTime := time.Now()
	h := syscall.MustLoadDLL("kernel32.dll")
	c := h.MustFindProc("GetDiskFreeSpaceExW")

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64

	ret, _, err := c.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(path))),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)))

	if ret == 0 {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to get Windows detailed disk usage: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryDiskUsage).
			Context("path", path).
			Context("operation", "get_detailed_disk_usage").
			Timing("detailed_disk_usage_check", time.Since(startTime)).
			Build()
		return DiskSpaceInfo{}, descriptiveErr
	}

	usedBytes := uint64(totalNumberOfBytes - totalNumberOfFreeBytes)

	// Record disk check duration if metrics are available
	if diskMetrics != nil {
		diskMetrics.RecordDiskCheckDuration(time.Since(startTime).Seconds())
	}

	return DiskSpaceInfo{
		TotalBytes: uint64(totalNumberOfBytes),
		UsedBytes:  usedBytes,
	}, nil
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
