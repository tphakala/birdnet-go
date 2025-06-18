//go:build linux || darwin
// +build linux darwin

package myaudio

import (
	"errors"
	"os/exec"
	"syscall"
)

// setupProcessGroup sets up a process group on Unix systems
func setupProcessGroup(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}
	}
}

// killProcessGroup kills a process and its children on Unix systems
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("invalid command or process is nil")
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid process ID")
	}

	// Kill the entire process group by sending SIGKILL to -pid
	// Negative PID means the signal is sent to every process in the process group
	return syscall.Kill(-pid, syscall.SIGKILL)
}
