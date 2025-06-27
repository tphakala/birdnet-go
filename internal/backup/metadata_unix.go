//go:build !windows
// +build !windows

package backup

import (
	"os"
	"syscall"
)

// getUnixMetadata gets Unix-specific file metadata
func getUnixMetadata(metadata *FileMetadata, info os.FileInfo) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		metadata.UID = int(stat.Uid)
		metadata.GID = int(stat.Gid)
	}
}
