//go:build darwin

package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/sys/unix"
)

// getMountInfoPlatform finds the mount point and filesystem type for Darwin (macOS) systems
func getMountInfoPlatform(path string) (*MountInfo, error) {
	// Clean and get absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// Use statfs to get mount point info
	var stat unix.Statfs_t
	if err := unix.Statfs(absPath, &stat); err != nil {
		return nil, fmt.Errorf("failed to get filesystem stats: %w", err)
	}

	// Convert filesystem type name from byte array to string
	fsTypeName := byteArrayToString(stat.Fstypename[:])
	mountPoint := byteArrayToString(stat.Mntonname[:])

	return &MountInfo{
		MountPoint:     mountPoint,
		FileSystemType: fsTypeName,
	}, nil
}

// byteArrayToString converts a null-terminated byte array to a Go string
func byteArrayToString(arr []byte) string {
	for i, b := range arr {
		if b == 0 {
			return strings.TrimSpace(string(arr[:i]))
		}
	}
	return strings.TrimSpace(string(arr))
}

// getInodeInfoPlatform gets inode information for Darwin systems
func getInodeInfoPlatform(path string) (*InodeInfo, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem stats for %s: %w", path, err)
	}

	return &InodeInfo{
		Free:  stat.Ffree,
		Total: stat.Files,
	}, nil
}

// captureMemoryInfo gathers system memory information for Darwin (macOS) systems
func captureMemoryInfo() (MemoryInfo, error) {
	info := MemoryInfo{}

	// Get virtual memory statistics using gopsutil (cross-platform)
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return info, fmt.Errorf("failed to get virtual memory stats: %w", err)
	}

	info.TotalBytes = vmStat.Total
	info.AvailableBytes = vmStat.Available
	info.UsedBytes = vmStat.Used
	info.UsedPercent = vmStat.UsedPercent

	// Get swap memory statistics
	swapStat, err := mem.SwapMemory()
	if err != nil {
		// Log warning but don't fail - some systems may not have swap
		info.SwapTotalBytes = 0
		info.SwapUsedBytes = 0
	} else {
		info.SwapTotalBytes = swapStat.Total
		info.SwapUsedBytes = swapStat.Used
	}

	return info, nil
}

// getProcessMemoryUsage gets memory usage for the current process on Darwin
func getProcessMemoryUsage() (*ProcessMemoryUsage, error) {
	// Get current process PID
	pid := int32(os.Getpid())

	// Get process instance
	proc, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get process instance: %w", err)
	}

	// Get memory info using gopsutil
	memInfo, err := proc.MemoryInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get process memory info: %w", err)
	}

	return &ProcessMemoryUsage{
		ResidentMB: int64(memInfo.RSS / 1024 / 1024),
		VirtualMB:  int64(memInfo.VMS / 1024 / 1024),
	}, nil
}
