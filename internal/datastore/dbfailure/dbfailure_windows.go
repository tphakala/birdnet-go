//go:build windows

package dbfailure

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"golang.org/x/sys/windows"
)

// isPlatformPathUnusable reports whether err is a Windows error that means the
// SQLite path cannot be used, the counterpart of the POSIX errnos classifyFile
// lists for that case.
//
// ERROR_DIRECTORY stands in for ENOTDIR (a parent that is a regular file, as
// GetDiskFreeSpaceExW reports it) and ERROR_WRITE_PROTECT for EROFS. The
// syscall constants do not match them on Windows: syscall.ENOTDIR is
// ERROR_PATH_NOT_FOUND and syscall.EROFS is an invented application error
// value that no Windows API returns.
func isPlatformPathUnusable(err error) bool {
	return errors.Is(err, windows.ERROR_DIRECTORY) || errors.Is(err, windows.ERROR_WRITE_PROTECT)
}

// isPlatformDiskFull reports whether err is a Windows error that means the
// volume is full, the counterpart of syscall.ENOSPC.
//
// syscall.ENOSPC is an invented application error value on Windows, so the
// real codes are matched here: ERROR_DISK_FULL and ERROR_HANDLE_DISK_FULL.
func isPlatformDiskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
