//go:build !windows

package dbfailure

// isPlatformPathUnusable reports false: on other platforms the syscall.ENOTDIR
// and syscall.EROFS checks in classifyFile already cover the unusable-path errors.
func isPlatformPathUnusable(error) bool { return false }

// isPlatformDiskFull reports false: on other platforms the syscall.ENOSPC check
// in classifyFile already covers a full disk.
func isPlatformDiskFull(error) bool { return false }
