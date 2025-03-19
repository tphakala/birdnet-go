//go:build windows
// +build windows

package myaudio

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// setupProcessGroup sets up a process group on Windows
func setupProcessGroup(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		}
	}
}

// killProcessGroup kills a process and its children on Windows
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("invalid command or process is nil")
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid process ID")
	}

	// First try graceful termination with taskkill
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid))

	// Set a 10-second timeout for the kill command
	killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer killCancel()
	killCmd = exec.CommandContext(killCtx, "taskkill", "/F", "/T", "/PID", fmt.Sprint(pid))

	if err := killCmd.Run(); err != nil {
		// If taskkill fails, try direct process termination
		return cmd.Process.Kill()
	}
	return nil
}
