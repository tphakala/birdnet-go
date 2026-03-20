// internal/restart/exec_unix.go
//go:build !windows

package restart

import (
	"os"
	"syscall"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Exec replaces the current process with a fresh instance of the same binary.
// On success this function never returns.
func Exec() error {
	binary, err := os.Executable()
	if err != nil {
		return errors.Newf("failed to resolve executable path: %w", err).
			Component("restart").
			Category(errors.CategorySystem).
			Build()
	}
	// syscall.Exec replaces the process image; same PID is preserved.
	return syscall.Exec(binary, os.Args, os.Environ())
}
