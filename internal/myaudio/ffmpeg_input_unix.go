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
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
