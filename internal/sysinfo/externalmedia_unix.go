//go:build !windows

package sysinfo

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// realMountProber is the production implementation of MountProber for POSIX systems.
// It compares device IDs of path and its parent to detect mountpoints.
func realMountProber(path string) MountProbeResult {
	result := MountProbeResult{}

	info, err := os.Stat(path)
	if err != nil {
		return result
	}
	result.Exists = true

	if !info.IsDir() {
		return result
	}

	// Check if path is a mountpoint by comparing its device ID to its parent.
	// A bind-mount or separate filesystem will have a different Dev value.
	var statPath, statParent syscall.Stat_t
	if err := syscall.Stat(path, &statPath); err != nil {
		return result
	}

	parent := filepath.Dir(filepath.Clean(path))
	if err := syscall.Stat(parent, &statParent); err != nil {
		return result
	}

	result.IsMountpoint = statPath.Dev != statParent.Dev

	// Check readability by opening the directory.
	f, err := os.Open(path) //nolint:gosec // path is a fixed constant, not user input
	if err != nil {
		return result
	}
	defer func() { _ = f.Close() }()

	_, err = f.Readdirnames(1)
	// io.EOF means the directory is empty but readable; nil means we got entries.
	result.Readable = err == nil || errors.Is(err, io.EOF)

	return result
}
