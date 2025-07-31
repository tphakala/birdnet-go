//go:build windows

package datastore

import (
	"fmt"
	"path/filepath"
	"unsafe"

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
		nil,                        // Volume name buffer
		0,                          // Volume name size
		nil,                        // Volume serial number
		nil,                        // Maximum component length
		nil,                        // File system flags
		&fsName[0],                 // File system name buffer
		uint32(len(fsName)),        // File system name size
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
func getInodeInfoPlatform(path string) (*InodeInfo, error) {
	return &InodeInfo{
		Free:  0,
		Total: 0,
	}, nil
}

// captureMemoryInfo gathers system memory information for Windows systems
func captureMemoryInfo() (MemoryInfo, error) {
	info := MemoryInfo{}
	
	// Get global memory status
	var memStatus windows.MemoryStatusEx
	memStatus.Length = uint32(unsafe.Sizeof(memStatus))
	
	err := windows.GlobalMemoryStatusEx(&memStatus)
	if err != nil {
		return info, fmt.Errorf("failed to get global memory status: %w", err)
	}

	info.TotalBytes = memStatus.TotalPhys
	info.AvailableBytes = memStatus.AvailPhys
	info.UsedBytes = info.TotalBytes - info.AvailableBytes
	info.UsedPercent = float64(memStatus.MemoryLoad)

	// Virtual memory (swap) information
	info.SwapTotalBytes = memStatus.TotalVirtual
	info.SwapUsedBytes = memStatus.TotalVirtual - memStatus.AvailVirtual

	return info, nil
}

// getProcessMemoryUsage gets memory usage for the current process on Windows
func getProcessMemoryUsage() (*ProcessMemoryUsage, error) {
	// Get current process handle
	process := windows.CurrentProcess()
	
	// Get process memory info
	var memCounters windows.ProcessMemoryCountersEx
	memCounters.Size = uint32(unsafe.Sizeof(memCounters))
	
	err := windows.GetProcessMemoryInfo(
		process,
		(*windows.ProcessMemoryCounters)(unsafe.Pointer(&memCounters)),
		memCounters.Size,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get process memory info: %w", err)
	}

	return &ProcessMemoryUsage{
		ResidentMB: int64(memCounters.WorkingSetSize / 1024 / 1024),
		VirtualMB:  int64(memCounters.PrivateUsage / 1024 / 1024),
	}, nil
}