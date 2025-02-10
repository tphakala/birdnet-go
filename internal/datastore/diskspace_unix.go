//go:build !windows

package datastore

import (
	"golang.org/x/sys/unix"
)

func getDiskFreeSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	if err != nil {
		return 0, err
	}

	// Available space in bytes
	return stat.Bavail * uint64(stat.Bsize), nil
}
