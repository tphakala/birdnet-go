// internal/restart/exec_windows.go
//go:build windows

package restart

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Exec starts a new instance of the binary and exits the current process.
// Windows lacks execve, so we spawn a detached child and exit.
func Exec() error {
	binary, err := os.Executable()
	if err != nil {
		return errors.Newf("failed to resolve executable path: %w", err).
			Component("restart").
			Category(errors.CategorySystem).
			Build()
	}
	cmd := exec.Command(binary, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	// Detach child so it survives parent exit.
	// CREATE_NEW_PROCESS_GROUP: new console group.
	// CREATE_BREAKAWAY_FROM_JOB: survive if parent runs in a Job Object
	// (Windows services, task scheduler). Silently ignored if not allowed.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x01000000, // CREATE_BREAKAWAY_FROM_JOB
	}
	if err := cmd.Start(); err != nil {
		return errors.Newf("failed to start new process: %w", err).
			Component("restart").
			Category(errors.CategorySystem).
			Build()
	}
	os.Exit(0)
	return nil // unreachable
}
