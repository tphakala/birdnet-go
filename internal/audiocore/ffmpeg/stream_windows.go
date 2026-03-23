//go:build windows

package ffmpeg

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
)

// setupProcessGroup sets up a process group on Windows.
func setupProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// killProcessGroup kills a process and its children on Windows.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	taskkillErr := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() //nolint:gosec // G204: PID from a process we started
	if taskkillErr != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			return fmt.Errorf("taskkill failed: %w; fallback kill also failed: %v", taskkillErr, killErr)
		}
		return nil // fallback kill succeeded
	}
	return nil
}
