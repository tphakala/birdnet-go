//go:build windows

package sysinfo

import (
	"errors"
	"io"
	"os"
)

// realMountProber is the Windows stub of MountProber. On Windows there is no
// bind-mount concept, so IsMountpoint is always false. Exists and Readable are
// still probed via the standard library.
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

	// IsMountpoint stays false on Windows (zero value; see the file-level note).

	// Check readability by opening the directory.
	f, err := os.Open(path) //nolint:gosec // path is a fixed constant, not user input
	if err != nil {
		return result
	}
	defer func() { _ = f.Close() }()

	_, readErr := f.Readdirnames(1)
	// An empty directory yields io.EOF from Readdirnames but is still readable.
	result.Readable = readErr == nil || errors.Is(readErr, io.EOF)
	return result
}
