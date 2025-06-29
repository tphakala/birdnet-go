//go:build !windows

package datastore

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func getDiskFreeSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	if err != nil {
		return 0, err
	}

	// Validate that Bsize is positive to avoid overflow when converting to uint64
	if stat.Bsize <= 0 {
		return 0, fmt.Errorf("datastore: invalid block size %d from filesystem", stat.Bsize)
	}

	// Available space in bytes
	bsize := uint64(stat.Bsize) // Bsize validated as positive, safe conversion
	return stat.Bavail * bsize, nil
}
