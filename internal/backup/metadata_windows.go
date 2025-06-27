//go:build windows
// +build windows

package backup

import (
	"os"
)

// getUnixMetadata is a no-op on Windows
func getUnixMetadata(metadata *FileMetadata, info os.FileInfo) {
	// No Unix metadata on Windows
}
