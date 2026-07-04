//go:build unix

package discovery

import (
	"os"
	"syscall"
)

// classifyOpenError maps a birds.db open failure to a candidate Reason. gorm and
// the SQLite driver return the same opaque "unable to open database file" for both
// an unreadable file and a corrupt or non-SQLite one, so this re-tests readability
// directly to tell a permission problem apart from a bad file.
//
// O_NONBLOCK prevents a FIFO or device node swapped in after Probe's Lstat from
// blocking the handler goroutine indefinitely. The returned fd is closed immediately
// if the open succeeds (we only need to test readability, not read data).
func classifyOpenError(dbPath string) string {
	f, err := os.OpenFile(dbPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if os.IsPermission(err) {
			return ReasonPermissionDenied
		}
		return ReasonOpenFailed
	}
	_ = f.Close()
	return ReasonOpenFailed
}
