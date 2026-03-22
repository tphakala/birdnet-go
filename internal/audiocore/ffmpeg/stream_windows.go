//go:build windows

package ffmpeg

import (
	"fmt"
	"os/exec"
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
	if err := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(cmd.Process.Pid)).Run(); err != nil { //nolint:gosec // G204: PID from a process we started
		return cmd.Process.Kill()
	}
	return nil
}
