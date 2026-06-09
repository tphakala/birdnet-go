//go:build linux

package embed

import "syscall"

// lowerPriority drops this process to the lowest CPU scheduling priority so
// a concurrently running live server always wins contention. Best effort.
func lowerPriority() {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19)
}
