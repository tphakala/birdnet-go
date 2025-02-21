//go:build linux || darwin
// +build linux darwin

package diskmanager

import (
	"syscall"
)

// GetDiskUsage returns the disk usage percentage for Unix-based systems (Linux and macOS)
func GetDiskUsage(baseDir string) (float64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(baseDir, &stat)
	if err != nil {
		return 0, err
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - free

	return (float64(used) / float64(total)) * 100, nil
}

// GetAvailableSpace returns the available disk space in bytes
func GetAvailableSpace(baseDir string) (uint64, error) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(baseDir, &stat)
	if err != nil {
		return 0, err
	}

	return stat.Bavail * uint64(stat.Bsize), nil
}
