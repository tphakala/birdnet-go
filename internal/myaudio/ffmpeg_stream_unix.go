//go:build linux || darwin
// +build linux darwin

package myaudio

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup sets up a process group on Unix systems
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// killProcessGroup kills a process and its children on Unix systems
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	// Ignore "no such process" errors as the process may have already exited
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
