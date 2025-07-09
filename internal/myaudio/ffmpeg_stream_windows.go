//go:build windows
// +build windows

package myaudio

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setupProcessGroup sets up a process group on Windows
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// killProcessGroup kills a process and its children on Windows
func killProcessGroup(cmd *exec.Cmd) error {
	// First try graceful termination with taskkill
	if err := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(cmd.Process.Pid)).Run(); err != nil {
		// If taskkill fails, try direct process termination
		return cmd.Process.Kill()
	}
	return nil
}
