package birdnet

import (
	"os"
	"path/filepath"
)

// CheckXNNPACKLibrary checks for the presence of libXNNPACK.a in typical Debian Linux library paths
func CheckXNNPACKLibrary() bool {
	libraryPaths := []string{
		"/usr/lib",
		"/usr/local/lib",
		"/lib",
		"/lib/x86_64-linux-gnu",
		"/usr/lib/x86_64-linux-gnu",
	}

	for _, path := range libraryPaths {
		fullPath := filepath.Join(path, "libXNNPACK.a")
		if _, err := os.Stat(fullPath); err == nil {
			// XNNPACK library is present
			return true
		}
	}

	return false
}
