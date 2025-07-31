//go:build windows

package datastore

import (
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// captureDiskSpace gathers comprehensive disk space information for Windows systems
func captureDiskSpace(dbPath string) (DiskSpaceInfo, error) {
	info := DiskSpaceInfo{}
	
	// Get the directory containing the database file
	dir := filepath.Dir(dbPath)
	
	// Convert to UTF16 for Windows API
	pathPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return info, fmt.Errorf("failed to convert path to UTF16: %w", err)
	}

	// Get disk space information
	var free, total, totalFree uint64
	err = windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree)
	if err != nil {
		return info, fmt.Errorf("failed to get disk space for %s: %w", dir, err)
	}

	info.TotalBytes = total
	info.AvailableBytes = free
	info.UsedBytes = total - free
	
	if info.TotalBytes > 0 {
		info.UsedPercent = float64(info.UsedBytes) / float64(info.TotalBytes) * 100.0
	}

	// Get volume information for filesystem type and mount point
	if volInfo, err := getVolumeInfo(dir); err == nil {
		info.MountPoint = volInfo.MountPoint
		info.FileSystemType = volInfo.FileSystemType
	} else {
		// Fallback to directory if volume info not available
		info.MountPoint = dir
		info.FileSystemType = "unknown"
	}

	// Note: Windows doesn't expose inode information as easily as Unix
	// Leave InodesFree and InodesTotal as 0 for Windows

	return info, nil
}

// VolumeInfo represents Windows volume information
type VolumeInfo struct {
	MountPoint     string
	FileSystemType string
}

// getVolumeInfo gets volume information for Windows
func getVolumeInfo(path string) (*VolumeInfo, error) {
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

	return &VolumeInfo{
		MountPoint:     rootPath,
		FileSystemType: windows.UTF16ToString(fsName[:]),
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