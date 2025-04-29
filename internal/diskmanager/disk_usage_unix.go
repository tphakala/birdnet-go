//go:build linux || darwin
// +build linux darwin

package diskmanager

import (
	"fmt"
	"syscall"
)

// GetDiskUsage returns the disk usage percentage for the given path
func GetDiskUsage(path string) (float64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return 0, fmt.Errorf("failed to get disk stats: %w", err)
	}

	// Calculate disk usage percentage
	totalBlocks := stat.Blocks
	freeBlocks := stat.Bfree
	usedBlocks := totalBlocks - freeBlocks
	usagePercent := float64(usedBlocks) / float64(totalBlocks) * 100.0

	return usagePercent, nil
}

// GetDetailedDiskUsage returns the total and used disk space in bytes for the filesystem containing the given path.
func GetDetailedDiskUsage(path string) (DiskSpaceInfo, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		return DiskSpaceInfo{}, fmt.Errorf("failed to statfs '%s': %w", path, err)
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize) // Available to non-root user
	usedBytes := totalBytes - freeBytes

	return DiskSpaceInfo{
		TotalBytes: totalBytes,
		UsedBytes:  usedBytes,
	}, nil
}
