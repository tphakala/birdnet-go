//go:build linux || darwin
// +build linux darwin

package diskmanager

import (
	"fmt"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// GetDiskUsage returns the disk usage percentage for the given path
func GetDiskUsage(path string) (float64, error) {
	startTime := time.Now()
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to get disk usage statistics: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryDiskUsage).
			Context("path", path).
			Timing("disk_usage_check", time.Since(startTime)).
			Build()
		return 0, descriptiveErr
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
	startTime := time.Now()
	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to get detailed disk statistics: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryDiskUsage).
			Context("path", path).
			Timing("detailed_disk_usage_check", time.Since(startTime)).
			Build()
		return DiskSpaceInfo{}, descriptiveErr
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize) // Available to non-root user
	usedBytes := totalBytes - freeBytes

	return DiskSpaceInfo{
		TotalBytes: totalBytes,
		UsedBytes:  usedBytes,
	}, nil
}
