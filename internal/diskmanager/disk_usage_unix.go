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
	usagePercentage := float64(usedBlocks) / float64(totalBlocks) * 100.0

	return usagePercentage, nil
}
