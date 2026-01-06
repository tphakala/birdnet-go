//go:build windows

package datastore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/sys/windows"
)

// getMountInfoPlatform gets mount point information for Windows systems
func getMountInfoPlatform(path string) (*MountInfo, error) {
	// Get the volume root path
	rootPath := filepath.VolumeName(path) + "\\"

	rootPathPtr, err := windows.UTF16PtrFromString(rootPath)
	if err != nil {
		return nil, err
	}

	// Buffer for filesystem name
	var fsName [windows.MAX_PATH + 1]uint16

	// Get volume information
	err = windows.GetVolumeInformation(
		rootPathPtr,
		nil,                 // Volume name buffer
		0,                   // Volume name size
		nil,                 // Volume serial number
		nil,                 // Maximum component length
		nil,                 // File system flags
		&fsName[0],          // File system name buffer
		uint32(len(fsName)), // File system name size
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get volume information: %w", err)
	}

	return &MountInfo{
		MountPoint:     rootPath,
		FileSystemType: windows.UTF16ToString(fsName[:]),
	}, nil
}

// getInodeInfoPlatform gets inode information for Windows systems
// Windows doesn't expose inode information easily, so return zeros
func getInodeInfoPlatform(_ string) (*InodeInfo, error) {
	return &InodeInfo{
		Free:  0,
		Total: 0,
	}, nil
}

// captureMemoryInfo gathers system memory information for Windows systems
func captureMemoryInfo() (MemoryInfo, error) {
	info := MemoryInfo{}

	// Get virtual memory statistics using gopsutil
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

// getProcessMemoryUsage gets memory usage for the current process on Windows
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
		ResidentMB: int64(memInfo.RSS / 1024 / 1024), // RSS = Resident Set Size (Working Set on Windows)
		VirtualMB:  int64(memInfo.VMS / 1024 / 1024), // VMS = Virtual Memory Size
	}, nil
}
