//go:build linux || darwin

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
		descriptiveErr := errors.Newf("diskmanager: failed to get disk usage statistics: %w", err).
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

// GetAvailableSpace returns the available disk space in bytes
func GetAvailableSpace(baseDir string) (uint64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(baseDir, &stat)
	if err != nil {
		return 0, err
	}

	// Validate that Bsize is positive to avoid overflow when converting to uint64
	if stat.Bsize <= 0 {
		return 0, fmt.Errorf("diskmanager: invalid block size %d from filesystem", stat.Bsize)
	}
	bsize := uint64(stat.Bsize) // Bsize validated as positive, safe conversion
	return stat.Bavail * bsize, nil
}

// GetDetailedDiskUsage returns the total and used disk space in bytes for the filesystem containing the given path.
func GetDetailedDiskUsage(path string) (DiskSpaceInfo, error) {
	startTime := time.Now()
	defer func() {
		// Always record disk check duration, even on error
		if m := getMetrics(); m != nil {
			m.RecordDiskCheckDuration(time.Since(startTime).Seconds())
		}
	}()

	var stat syscall.Statfs_t
	err := syscall.Statfs(path, &stat)
	if err != nil {
		descriptiveErr := errors.Newf("diskmanager: failed to get detailed disk statistics: %w", err).
			Component("diskmanager").
			Category(errors.CategoryDiskUsage).
			Context("path", path).
			Timing("detailed_disk_usage_check", time.Since(startTime)).
			Build()
		return DiskSpaceInfo{}, descriptiveErr
	}

	// Validate that Bsize is positive to avoid overflow when converting to uint64
	if stat.Bsize <= 0 {
		descriptiveErr := errors.Newf("diskmanager: invalid block size %d from filesystem", stat.Bsize).
			Component("diskmanager").
			Category(errors.CategoryDiskUsage).
			Context("path", path).
			Context("bsize", stat.Bsize).
			Timing("detailed_disk_usage_check", time.Since(startTime)).
			Build()
		return DiskSpaceInfo{}, descriptiveErr
	}

	bsize := uint64(stat.Bsize) // Bsize validated as positive, safe conversion
	totalBytes := stat.Blocks * bsize
	freeBytes := stat.Bavail * bsize // Available to non-root user
	usedBytes := totalBytes - freeBytes

	return DiskSpaceInfo{
		TotalBytes: totalBytes,
		UsedBytes:  usedBytes,
	}, nil
}
